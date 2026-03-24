# 履歷呈現重點 (Resume Highlights)

> 撰寫原則：**成果導向 (Impact-driven)**，不只列技術堆疊，而是寫出「用這些技術解決了什麼問題」。

---

## 1. 架構演進與流量削峰 (Architecture Evolution & Traffic Shaping)

**履歷條目**：
> 主導撮合系統從單體同步架構重構至基於 Kafka 的非同步事件驅動微服務架構，將下單寫入與撮合結算解耦，實現流量削峰填谷。透過 Partition Key 機制確保同一交易對的事件嚴格有序，同時支援系統動態水平擴展。

**背後技術**：
- 微服務拆分：Gateway / Order Service / Matching Engine / Market Data Service
- Kafka 非同步事件流：`exchange.orders` → 撮合 → `exchange.settlements` → 結算
- Redis 共享 OrderBook Snapshot，彌補跨服務不共享記憶體的問題

---

## 2. 壓力測試與效能驗證 (Stress Testing & Performance Validation)

**履歷條目**：
> 設計並執行多層次 k6 壓力測試（Smoke / Load / Spike / WebSocket Fanout），在本地與 ECS 雲端環境中驗證架構正確性。Spike 測試中 800 VU 瞬間湧入產生逾 3,000 TPS 壓力，系統透過 Token Bucket 限流器精準攔截 85% 超額流量，實現 0% 崩潰率。

**資料量維度的加分論述**：
> 不僅模擬突發大流量，更在 PostgreSQL 預先植入海量歷史數據的情境下進行測試，精準定位大資料量下的 I/O 瓶頸。證明「空資料庫的高 QPS」與「百萬筆訂單後的高 QPS」瓶頸完全不同，並以此驅動 CQRS 與快取策略的架構決策。

**關鍵數據**：
| 測試類型 | 場景 | 關鍵結果 |
|:---|:---|:---|
| Spike Test | 800 VU 突發湧入 | 3,000+ TPS, 85% 超額攔截, 0% Downtime |
| WS Fanout | 2,000 長連線 | 60,000 msg/s 扇出推播, 99.9% 連線成功率 |
| Load Test | 100 VU × 2 分鐘 | P95 < 1s, 5xx < 0.1% |
| Correctness Audit | 高併發壓測後 | Balance + Locked 100% 帳目相符，零誤差 |

---

## 3. 金融級資料一致性 (Financial-Grade Data Consistency)

**履歷條目**：
> 實作兩階段非同步結算架構（TX1 鎖資金 + TX2 冪等結算），搭配字典序加鎖防死鎖演算法與基於 Trade ID 的冪等性防禦。經長時間極限壓力測試，系統在高併發下資金快照總額達到 100% 帳目相符，徹底根除 Race Condition 與 Lost Update。

**三道防線**：
1. **事前預扣 (Funds Locking)**：下單即在 DB 內將資金移入 locked 欄位，杜絕超額下單
2. **序列化防死鎖 (Ordered Locking)**：結算時將所有 Order ID 排序後依序取得排他鎖
3. **冪等性防禦 (Idempotency)**：基於 Trade ID 的 Unique Key，確保 Kafka 重複投遞不會重複扣款

---

## 4. 即時行情巨量推播 (High-Concurrency WebSocket Fanout)

**履歷條目**：
> 利用 Go Goroutine 輕量級特性設計無鎖 WebSocket 廣播機制，將 WebSocket 推播從交易核心抽離為獨立的 Market Data Service。單一節點維持 2,000 個高頻長連線，達成 60,000 筆/秒行情扇出推播，P95 延遲低於 200ms。

---

## 5. 雲原生部署與 DevOps (Cloud Native & CI/CD)

**履歷條目**：
> 建置基於 GitHub Actions 與 GitOps 的 CI/CD 工作流，為四個微服務（Gateway, Order, Matching, Market Data）實現自動化建置、推送至 GHCR 並透過 ArgoCD 同步部署至 K3s 叢集。

---

## 6. 記憶體內撮合引擎 (In-Memory Matching Engine)

**履歷條目**：
> 開發支援價格/時間優先 (Price-Time Priority) 的高效能記憶體撮合引擎，完整支援市價/限價單及部分成交邏輯。啟動時從 DB 恢復活動訂單至記憶體，並預熱 Redis OrderBook 快取，確保冷啟動後首筆市價單即可正確估算資金。
