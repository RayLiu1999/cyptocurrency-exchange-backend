# 測試實作步驟文件

本文件是實際執行 runbook。內容只保留「照著做就能執行」的步驟，不再重複解釋本地與線上各自的責任邊界。

## 0. 總目標

本 runbook 的總目標，是把測試工作拆成可落地執行的步驟，並明確區分：

1. 現在已經可以直接做的事情。
2. 文件要求但尚未補齊的事情。
3. 哪些步驟做完後，才有資格對 correctness、容量與韌性下結論。

完整 runbook 要能支撐以下最終目標：

1. 確認本地 correctness 已收斂。
2. 確認 staging / ECS 上的容量邊界。
3. 確認資料量成長不會讓結論失真。
4. 確認壓測後金額與狀態一致性仍正確。
5. 確認依賴故障與長時間運行下仍可恢復與穩定。

## 0.1 目前已具備與尚未具備

### 已具備

| 項目                                  | 現況                       |
| :------------------------------------ | :------------------------- |
| 本地 Go 測試命令                      | 已具備                     |
| 本地 k6 baseline 命令                 | 已具備                     |
| ECS 部署與手動指定 `BASE_URL` 執行 k6 | 已具備                     |
| 基本 runbook 順序                     | 已具備，可作為手動執行骨架 |

### 尚未具備

| 項目                       | 缺口                                            |
| :------------------------- | :---------------------------------------------- |
| correctness audit 實作步驟 | 文件有要求，但缺實際 SQL / script               |
| 資料量分層準備步驟         | 文件有要求，但缺 generator / dataset workflow   |
| 韌性測試操作步驟           | 文件有要求，但缺 chaos / failure injection 工具 |
| soak test 操作包裝         | 文件有要求，但缺專用腳本與驗收門檻              |
| 測試結果自動彙整           | 尚無統一報告輸出與 pass / fail gate             |

## 1. 目標

依序完成：

1. 本地 correctness 與 baseline。
2. staging / ECS baseline。
3. 單一 symbol 容量測試。
4. 資料量分層測試。
5. correctness audit。
6. 韌性測試。
7. soak test。

目前狀態可解讀為：

1. 第 1 與第 2 步大致可落地執行。
2. 第 3 步可手動執行，但還沒有完整標準化。
3. 第 4 到第 7 步在文件中已定義目標，但多數仍待補實作。

## 2. 前置條件

### 2.1 本地前置條件

1. `Makefile` 指令可用。
2. 本地 PostgreSQL / Redis / Kafka 或對應 dev 環境可連線。
3. 服務可正常啟動。
4. k6 已安裝。

### 2.2 staging / ECS 前置條件

1. Gateway 與相關服務已部署。
2. metrics endpoint 可被 Prometheus / CloudWatch 觀測。
3. 壓測用資料已準備完成。
4. 已記錄本次測試的資料量級別。

## 3. 本地執行順序

### Step 1: Go 測試

```bash
make test
make test-integration
make test-race
make test-all
```

### Step 2: k6 baseline

```bash
make smoke-test
make load-test
make spike-test
make ws-fanout-test
```

#### 本地 k6 監控看板指引

本地執行時主要看 k6 自身的 terminal 輸出，不依賴 Grafana。以下是各腳本執行時應確認的數字：

**smoke-test（冒煙）**

| k6 指標                    | 目標      | 判斷                        |
| :------------------------- | :-------- | :-------------------------- |
| `http_req_failed`          | = 0%      | 任何失敗都要檢查            |
| `http_req_duration{p(95)}` | < 500ms   | 超過代表服務很慢            |
| 所有 checks                | 100% pass | 失敗代表 response body 異常 |

**load-test（負載，100 VU × 2 分鐘）**

| k6 指標                    | 目標   | 判斷                                    |
| :------------------------- | :----- | :-------------------------------------- |
| `http_req_failed`          | < 1%   | 超過需檢查是否有 5xx                    |
| `http_req_duration{p(95)}` | < 1s   | 超過代表本地已有明顯瓶頸                |
| `http_req_duration{p(99)}` | < 3s   | 超過需確認是 timeout 或真實延遲         |
| `iterations` 總量          | 合理值 | 與 VU × duration 對比，確認沒有大量阻塞 |

**spike-test（尖峰，10→800 VU）**

| k6 指標                           | 目標                     | 判斷                 |
| :-------------------------------- | :----------------------- | :------------------- |
| `http_req_failed`                 | < 5%（尖峰期可接受 429） | 5xx 超過 1% 需調查   |
| 尖峰後 `http_req_duration{p(95)}` | 應回落接近基線           | 若持續飆高代表未恢復 |

**ws-fanout-test（WebSocket 扇出，2000 連線）**

| k6 指標                           | 目標  | 判斷                       |
| :-------------------------------- | :---- | :------------------------- |
| WebSocket 連線建立成功率          | > 99% | 失敗代表連線上限或資源問題 |
| 連線存活期間是否收到 ping/message | 是    | 未收到代表廣播失效         |

### Step 3: 本地結果確認

確認：

1. 基本 correctness 無明顯錯誤。
2. 無 data race。
3. 無明顯 500 / panic。
4. baseline latency 與 error rate 可接受。

以上 3 個 step 屬於目前已能直接執行的範圍。

#### 本地結果記錄方式

本地跑完後，依 `TEST_REPORT_TEMPLATE.md` 填入以下欄位：

1. 環境：`local`
2. 測試類型：`smoke` / `load` / `spike` / `ws-fanout`
3. 效能結果（k6 terminal 數字）
4. correctness 結果：`make test-all` 及 `make test-race` 的通過狀態
5. 本輪結論：可以說「本地功能 correctness 初步成立」，不可以說「系統能扛 XX TPS」

## 4. staging / ECS 執行順序

### Step 1: ECS smoke / baseline

```bash
make smoke-test BASE_URL=http://<ALB_OR_GATEWAY_URL>/api/v1
make load-test BASE_URL=http://<ALB_OR_GATEWAY_URL>/api/v1
```

#### 監控看板指引（ECS baseline）

啟動 Grafana 並開啟 `Exchange Load Testing Dashboard`，在壓測執行期間依序確認：

**Step 1-A：確認流量已進入（smoke 期間）**

打開 **HTTP RPS** panel，確認數字從 0 開始上升，表示請求有打到服務。若 RPS 維持 0，代表 BASE_URL 錯誤或 ALB health check 未通過。

**Step 1-B：確認無 5xx（load 開始後）**

打開 **HTTP 5xx Error Rate** panel：

- 目標 < 0.1%
- 若超過 1%，立即暫停，檢查 ECS logs（`make ecs-logs`）

**Step 1-C：觀察 P95 latency 趨勢**

打開 **HTTP P95 Latency** 與 **HTTP P95 by Service** panels：

- baseline 期間 P95 應穩定（不應持續上升）
- 若 P95 在低 VU 時就已 > 1s，代表服務本身有問題，不是容量問題

**Step 1-D：確認訂單有被處理**

打開 **Order Throughput** panel：

- 應可見穩定的 order/s 數字
- 若為 0，代表 order-service 或 matching-engine 有問題

**Step 1-E：確認 Kafka 事件正常流動**

打開 **Kafka Event Throughput** panel：

- 各 component（order_consumer、settlement_consumer）應有事件流量
- 若某 component 為 0，代表對應 consumer 可能未啟動或 lag 已積壓

記錄：

1. P50 / P95 / P99
2. error rate
3. ECS task CPU / memory（從 AWS Console CloudWatch）
4. DB active connections（手動查詢：`SELECT count(*) FROM pg_stat_activity WHERE state = 'active';`）

### Step 2: 單一交易對容量測試

```bash
make load-test \
  BASE_URL=http://<ALB_OR_GATEWAY_URL>/api/v1 \
  SYMBOL=BTC-USD \
  K6_ENV_FLAGS="--vus 200 --duration 30m"
```

目標：

1. 找穩定 TPS / RPS。
2. 找 P95 / P99 惡化拐點。
3. 找第一個瓶頸。

#### 監控看板指引（容量測試）

容量測試的核心是找「拐點」：在什麼 VU 數量之後，P95 latency 開始非線性惡化。

**開始前**：記錄目前資料量背景（orders / trades 總量、表大小）。

**執行中，依序觀察**：

1. **HTTP P95 by Service**：VU 增加時，哪個服務的 P95 先惡化？
   - Gateway 先惡化 → 入口層問題（連線池、路由）
   - Order Service 先惡化 → 業務層問題（鎖競爭、DB 查詢）
   - Matching Engine 先惡化 → 撮合層問題（單一 goroutine 瓶頸）

2. **Order Processing P95**：應與 HTTP P95 走勢相關，若 Order P95 遠高於 HTTP P95，代表瓶頸在 order 處理路徑（DB lock / async queue）。

3. **Kafka Event Throughput**：Kafka event 量應與 order 量正比。若 order 量增加但 Kafka event 沒有對應增加，代表 event publish 已成為瓶頸。

4. **HTTP 5xx Error Rate**：壓力上升時 5xx 開始出現，代表已超過服務可處理能力，此時 VU 數即為容量上限附近。

5. **Trades Executed**：成交量應隨訂單量增加。若成交量停止增長但訂單量仍增加，代表 matching engine 已積壓。

**拐點判定標準**：P95 從穩定期的基線值超過 2 倍，即視為拐點。記錄此時的 VU 數。

**Hand-check（無 Kafka lag panel 時替代）**：

```sql
-- 確認 Kafka 沒有大量積壓（需在 Redpanda / Kafka 工具中查閱）
-- 或觀察 DB 中 order 與 trade 的比例是否正常
SELECT COUNT(*) FROM orders WHERE created_at > NOW() - INTERVAL '1 minute';
SELECT COUNT(*) FROM trades WHERE created_at > NOW() - INTERVAL '1 minute';
```

這一步目前可手動執行，但若沒有固定資料量背景與報告格式，結果仍偏向探索用途。

### Step 3: 資料量分層測試

至少針對兩種資料量級別各做一次：

1. S 或 M
2. M 或 L

每次都記錄：

1. `orders` / `trades` / `accounts` 總量
2. 活躍 orderbook depth
3. 表與索引大小
4. 查詢與 snapshot restore 延遲

#### 資料量分層時的監控看板指引

資料量分層測試的目標是對比不同資料量下，相同 VU 數的結果是否惡化。

**對比指標（每個資料量等級都填同一份 `TEST_REPORT_TEMPLATE.md` 中的表格）**：

| 階段                            | S 級 | M 級 | L 級 | 趨勢               |
| :------------------------------ | :--- | :--- | :--- | :----------------- |
| HTTP P95 latency                |      |      |      | 是否隨資料量上升？ |
| Order Processing P95            |      |      |      |                    |
| Kafka Event P95                 |      |      |      |                    |
| Snapshot restore 時間（重啟後） |      |      |      |                    |

**關鍵查詢（每個資料量等級執行前後都要跑）**：

```sql
-- 表大小
SELECT relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
ORDER BY pg_total_relation_size(c.oid) DESC;

-- 最慢的查詢
SELECT query, mean_exec_time, calls
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
LIMIT 5;
```

**注意**：若無資料生成器，此步驟無法標準化執行，只能記為「待補」。

這一步目前屬於目標已定義、落地未完成，因為還沒有對應資料生成器與標準化資料準備流程。

### Step 4: correctness audit

每輪壓測後都做：

1. 資產總量守恆檢查
2. `balance + locked` 一致檢查
3. stuck order 檢查
4. trade / order 對帳檢查

若 audit 失敗，該輪壓測直接視為失敗。

#### correctness audit 執行步驟

每輪壓測結束後（k6 已停止），依序執行以下 SQL，並把結果填入 `TEST_REPORT_TEMPLATE.md` 第 3 節：

```sql
-- 1. 資產守恆
SELECT currency, SUM(balance + locked) AS total
FROM accounts
GROUP BY currency;
-- ➜ 與壓測前比較，總量應完全一致

-- 2. balance + locked 一致（找出鎖但沒有 open order 的帳戶）
SELECT a.user_id, a.currency, a.locked
FROM accounts a
WHERE a.locked > 0
  AND NOT EXISTS (
    SELECT 1 FROM orders o
    WHERE o.user_id = a.user_id
      AND o.status IN ('NEW', 'PARTIALLY_FILLED')
  );
-- ➜ 應為 0 筆

-- 3. Stuck order
SELECT id, user_id, symbol, status, created_at
FROM orders
WHERE status IN ('NEW', 'PARTIALLY_FILLED')
  AND created_at < NOW() - INTERVAL '10 minutes';
-- ➜ 應為 0 筆（或合理的未完成訂單）

-- 4. Trade / Order 對帳
SELECT o.id, o.filled_quantity,
       COALESCE(SUM(t.quantity), 0) AS trade_total
FROM orders o
LEFT JOIN trades t ON (t.maker_order_id = o.id OR t.taker_order_id = o.id)
WHERE o.status = 'FILLED'
GROUP BY o.id, o.filled_quantity
HAVING ABS(o.filled_quantity - COALESCE(SUM(t.quantity), 0)) > 0.000001;
-- ➜ 應為 0 筆
```

這一步目前屬於高優先缺口，因為尚未具備可直接執行的 SQL / script（以上 SQL 為初步版本，尚需整合成可自動執行的 script）。

### Step 5: 韌性測試

依序執行：

1. Kafka 暫停 / 恢復
2. Redis 暫停 / 恢復
3. PostgreSQL 注入延遲
4. WebSocket reconnect storm

#### 監控看板指引（韌性測試）

每個故障情境的執行步驟：

1. **確認基線**：先讓服務在正常 load 下穩定運行 2 分鐘，記錄 P95、error rate。
2. **注入故障**：執行對應的故障操作（見下方）。
3. **觀察降級**：記錄故障後服務的行為（error rate 上升多少？P95 變化？）
4. **恢復**：移除故障。
5. **觀察恢復**：記錄服務從故障到完全恢復正常需要多久。

**Kafka 暫停（Docker 環境）**：

```bash
docker pause redpanda  # 或 docker pause kafka
# 觀察：Kafka Event Throughput → 降為 0；Order Throughput → 應降低或出現 error
# 等待 60 秒後：
docker unpause redpanda
# 觀察：Kafka Event Throughput → 應自動恢復；積壓的 event 是否被消費完
```

**Redis 暫停**：

```bash
docker pause redis
# 觀察：若 redis 是 orderbook cache，HTTP P95 應上升（每次需重建 snapshot）
# 等待 30 秒後：
docker unpause redis
# 觀察：P95 是否恢復
```

**PostgreSQL 延遲模擬**：

```bash
# 透過 tc 注入延遲（需 root 或容器權限）
# 或直接在 Postgres 中執行慢查詢來佔用連線
# 替代方案：手動在 DB 執行 pg_sleep 佔用連線池
SELECT pg_sleep(30);  -- 多個 session 同時執行以佔用連線
```

**應觀察的 Grafana panel**：

- `HTTP 5xx Error Rate`：注入期間應上升，恢復後應回落
- `Kafka Event Throughput`：Kafka pause 期間為 0，恢復後要確認 lag 會被消耗完
- `WebSocket Broadcast Loss Rate`：系統壓力大時廣播丟包是否增加

**韌性測試結果記錄**：

| 故障類型    | 降級時 error rate | 恢復耗時 | 資料一致性（audit） | 判斷 |
| :---------- | :---------------- | :------- | :------------------ | :--- |
| Kafka pause |                   |          |                     |      |
| Redis pause |                   |          |                     |      |
| DB 高延遲   |                   |          |                     |      |

這一步目前屬於文件目標，尚未具備實作工具與標準化操作手順。

### Step 6: Soak Test

依序執行：

1. 30 分鐘 sustained load
2. 24 小時 soak test

持續追蹤：

1. heap
2. goroutine
3. DB latency
4. Kafka lag
5. WS disconnect rate

#### 監控看板指引（Soak Test）

Soak test 的重點不是看瞬間值，而是看趨勢。以下是各指標的正常與異常徵兆：

**Grafana 開啟 time range：1h 或 24h，觀察趨勢線而非瞬間值。**

| 指標                          | 正常       | 異常徵兆                        |
| :---------------------------- | :--------- | :------------------------------ |
| HTTP P95 Latency              | 穩定平行線 | 緩慢上升（每小時 +10ms 以上）   |
| Order Processing P95          | 穩定       | 隨時間增加                      |
| Kafka Event P95               | 穩定       | 緩慢上升（積壓信號）            |
| WebSocket Broadcast Loss Rate | < 1%，穩定 | 緩慢上升（WS disconnect 累積）  |
| WebSocket Connections         | 穩定       | 緩慢下降（connection 未被重建） |

**目前尚未具備的觀測**（需補 Go runtime metrics）：

| 指標            | 正常             | 異常徵兆                       |
| :-------------- | :--------------- | :----------------------------- |
| Goroutine count | 穩定             | 線性增長（goroutine leak）     |
| Heap alloc      | 穩定或有 GC 波動 | GC 後基線仍上升（memory leak） |

**替代手段**（無 Go runtime metrics 時）：

```bash
# 每 5 分鐘從 ECS 取一次 runtime 資訊（需要實作 /debug/vars 或 pprof endpoint）
curl http://<service-url>/debug/vars | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('cmdline'), d.get('memstats'))"
# 或透過 AWS CloudWatch 看 ECS task memory utilization 趨勢
```

**Soak Test 結束後必做**：

1. 執行完整 correctness audit（四個 SQL）
2. 比對壓測開始前後的 DB 表大小
3. 確認 Kafka consumer group 的 lag 已歸零

這一步目前可以用既有 k6 腳本延長執行時間做手動版本，但尚未形成完整 soak test 套件。

## 5. 資料量與環境對應

| 級別 | 建議環境           | 用途                                      |
| :--- | :----------------- | :---------------------------------------- |
| S    | 本地               | baseline、腳本驗證、基本 correctness      |
| M    | 本地或 staging     | query / index / snapshot 第一輪問題       |
| L    | staging / ECS      | 真實容量、恢復時間、DB cache / index 問題 |
| XL   | 線上或專用壓測環境 | 長期成長風險與最終容量驗證                |

## 6. 每輪測試最少輸出

| 類別             | 內容                                                        |
| :--------------- | :---------------------------------------------------------- |
| 環境             | local / staging / ECS                                       |
| 測試版本         | commit / image tag                                          |
| 資料量背景       | `orders` / `trades` / `accounts`、orderbook depth、索引大小 |
| 效能結果         | RPS、TPS、P50、P95、P99、error rate                         |
| correctness 結果 | 資產守恆、stuck order、對帳結果                             |
| 依賴層結果       | DB、Kafka、Redis、WS 指標                                   |
| 結論             | 第一個瓶頸、資料量風險、下一步動作                          |

## 7. 完成條件

完整測試流程至少要能回答：

1. 本地 correctness 是否已收斂。
2. staging / ECS 上的容量邊界在哪裡。
3. 資料量變大時瓶頸是否改變。
4. 故障後是否能恢復。
5. 長時間運行是否穩定。

如果目前只能穩定完成本地步驟與 baseline 壓測，卻無法完成 correctness audit、資料量分層、韌性與 soak test，則 runbook 只能算完成第一階段，不能視為完整驗收流程。

---

## 8. 測試結果整理方式

### 8.1 每輪測試填寫報告

每輪壓測完成後，複製 `TEST_REPORT_TEMPLATE.md` 並依以下命名規則存放：

```
docs/testing/results/YYYY-MM-DD_<env>_<test-type>_<data-tier>.md

例：
  docs/testing/results/2026-03-24_local_load_S.md
  docs/testing/results/2026-03-24_ECS_capacity_M.md
  docs/testing/results/2026-03-24_ECS_soak_L.md
```

報告內容必填欄位：

- 環境、資料量等級、Git commit
- 效能數字（P50 / P95 / P99、error rate）
- correctness audit 四個 SQL 的結果
- 本輪結論（能下什麼結論 / 不能下什麼結論）
- 下一步動作（具體動作，而非方向）

### 8.2 各測試類型決策規則

| 測試類型          | 通過條件                                             | 若未通過怎麼辦                  |
| :---------------- | :--------------------------------------------------- | :------------------------------ |
| smoke             | 所有 k6 checks 100% pass，無 5xx                     | 檢查服務 log，修復後重跑        |
| load baseline     | P95 < 1s，5xx < 1%，correctness audit 通過           | 先修 correctness，再看效能      |
| spike             | 5xx < 5%（尖峰期），P95 在流量降低後恢復             | 確認是否有 goroutine / 連接洩漏 |
| ws-fanout         | 2000 連線成功建立，Loss Rate < 1%                    | 調整 WS buffer size 或連線上限  |
| 容量測試          | 找到 P95 > 2x 基線的拐點，並知道哪一層先爆           | 下一步優化該層                  |
| correctness audit | 四個 SQL 全部 0 筆異常                               | **整輪壓測視為失敗，優先修復**  |
| 韌性測試          | 故障恢復後 error rate 回到基線，correctness 通過     | 確認 retry / reconnect 邏輯     |
| soak test         | 24h 後 P95 / goroutine / heap 無明顯漂移，audit 通過 | 查慢性洩漏原因                  |

### 8.3 如何看 Grafana 截圖

每份報告的「附件」欄位應包含以下 Grafana 截圖（time range 對應測試時段）：

1. HTTP P95 Latency 趨勢圖（完整測試期間）
2. HTTP 5xx Error Rate 趨勢圖
3. Order Throughput 趨勢圖
4. Kafka Event Throughput 趨勢圖
5. WebSocket Broadcast Loss Rate（若有 WS 測試）

截圖命名建議：`<panel-name>_<date>_<start-time>_<end-time>.png`

### 8.4 監控缺口的替代記錄

目前 Grafana 缺少 PostgreSQL、Redis、Go runtime、Kafka lag 等指標。在這些 exporter 補齊前，每輪壓測需手動記錄：

| 指標                  | 手動取得方式                                                                                 |
| :-------------------- | :------------------------------------------------------------------------------------------- |
| DB active connections | `SELECT count(*) FROM pg_stat_activity WHERE state = 'active';`                              |
| DB slow query         | `SELECT query, mean_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 5;` |
| Kafka consumer lag    | Redpanda Console UI 或 `rpk group describe <group>`                                          |
| Go heap / goroutine   | ECS CloudWatch memory，或在服務加 `/debug/pprof` endpoint                                    |

詳細的缺口清單見 `grafana/README.md`。
