# 壓力測試腳本說明

本目錄收錄針對加密貨幣交易所後端設計的 k6 壓力測試腳本，每個場景都有明確的「測試目的」與「預期結論」，確保壓測結果能夠**支撐架構決策**，而非只是跑出一份通過報告。

## 執行環境

```bash
# 確保後端服務已啟動
make prod-up  # 或 docker-compose up

# 安裝 k6（macOS）
brew install k6
```

---

## 場景總覽

| 腳本 | 場景目的 | 核心問題 |
|:---|:---|:---|
| `smoke-test.js` | 環境健康檢查 | 服務有沒有跑起來？ |
| `load-test.js` | 基礎負載測試 | 100 VU 下 P95 < 100ms 嗎？ |
| `spike-test.js` | 突發尖峰測試（限流驗證）| 800 VU 衝入時系統能優雅降級嗎？ |
| `ws-fanout-test.js` | WebSocket 廣播基礎測試 | 2000 連線能否全部建立成功？ |
| `matching-engine-capacity-test.js` | **撮合引擎容量拐點分析** | TPS 到多少時第一個瓶頸出現？在哪層？ |
| `hot-vs-multi-symbol-test.js` | **熱門交易對 vs 多交易對吞吐對比** | Kafka Partition 分散能提升多少吞吐量？ |
| `market-storm-test.js` | **行情風暴（下單 + WebSocket 同時壓測）** | WebSocket 廣播會拖慢下單 P95 嗎？ |

---

## 深度場景詳解

### 場景 A：撮合引擎容量測試（matching-engine-capacity-test.js）

```bash
# 打 Gateway（完整鏈路）
BASE_URL=http://localhost:8100/api/v1 k6 run scripts/k6/matching-engine-capacity-test.js
```

**測試邏輯**：VU 從 10 → 50 → 100 → 200 逐步爬升，持續下限價單，觀察 P95/P99 延遲何時開始退化。

**自訂指標**：
| 指標 | 說明 |
|:---|:---|
| `exchange_order_success_rate` | 有效訂單比率（排除 429 限流）|
| `exchange_order_p99_latency_ms` | 下單 P99 延遲（找出尾延遲壓力點）|
| `exchange_order_total_count` | 總下單數（推算整體 TPS）|

**預期結論**：P99 在某個 VU 下出現崖式上升，同時觀察 Grafana 的 `exchange_db_wait_count_total`，可定位瓶頸在 DB 連線池還是 Kafka 背壓。

---

### 場景 B：熱門交易對 vs 多交易對吞吐對比（hot-vs-multi-symbol-test.js）

```bash
# 跑 1：全部集中在 BTC-USD（熱門模式）
SYMBOL_MODE=hot k6 run scripts/k6/hot-vs-multi-symbol-test.js

# 跑 2：分散到 5 個交易對（多 Kafka Partition）
SYMBOL_MODE=multi k6 run scripts/k6/hot-vs-multi-symbol-test.js
```

**測試邏輯**：固定 100 VU × 2 分鐘，唯一差異是 Symbol 選擇策略。

**預期結論**：
> "多交易對（5 Symbols）模式下，P95 延遲比單一 BTC-USD 模式低 XX%，
> 驗證了 Kafka Partition Key = Symbol 的設計能有效分散熱點，
> 避免單一熱門交易對成為全系統瓶頸。"

---

### 場景 C：行情風暴測試（market-storm-test.js）

```bash
WS_URL=ws://localhost:8100/ws BASE_URL=http://localhost:8100/api/v1 \
  k6 run scripts/k6/market-storm-test.js
```

**測試邏輯**：兩個 k6 scenario 同時跑：
- `orderers`：50 VU 持續下單，製造行情變化觸發廣播
- `watchers`：1000 個 WebSocket 連線，接收廣播訊息並計數

**自訂指標**：
| 指標 | 說明 |
|:---|:---|
| `exchange_ws_connect_success_rate` | WebSocket 連線成功率 |
| `exchange_ws_messages_received` | 廣播訊息接收總數（計算廣播 TPS）|
| `exchange_order_latency_ms` | 下單延遲（驗證廣播不影響下單效能）|

**預期結論**：
> "在 1,000 個 WebSocket 長連線同時接收行情的情況下，
> 下單 P95 延遲仍維持在 300ms 以下，
> 驗證 Market Data Service 的資源隔離設計有效，即便行情風暴也不拖累交易核心。"

---

## 截圖存放位置

壓測完成後，將 k6 終端機輸出截圖存放至：

```
docs/testing/stresstest-results/
├── scenario-A-capacity-test.png
├── scenario-B-hot-symbol.png
├── scenario-B-multi-symbol.png
└── scenario-C-market-storm.png
```
