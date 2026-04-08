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

## 2. 極速批次寫入與造市商支援 (Ingestion Batching & Market Maker Support)

**履歷條目**：
> 針對造市商高頻寫入瓶頸，實作 `POST /orders/batch` 批次下單通道。底層整合 PostgreSQL 原生 `pgx.CopyFrom` 協定進行極速 I/O，並在 Go 應用層開發「UserID 與 Currency 二維記憶體排序」死結防禦演算法。成功在高併發壓測中展現 10 倍於 HTTP 獨立請求的寫入吞吐量且達成 0 死結。

**背後技術**：
- 資料庫零負擔寫入：摒棄迴圈 `INSERT`，採取 Bulk Copy 機制。
- 記憶體預排序：將大量雜亂的資源請求，在進入資料庫 Row Lock 爭搶前，先在記憶體中完成全域排隊。

---

## 3. 生產級壓力測試與系統韌性 (Production-Grade K6 Stress Validation)

**履歷條目**：
> 捨棄傳統盲測，自行定義並執行四大象限 K6 壓測架構（E2E Latency、Ingestion Batch、Market Storm 隔離測試、Spike 突波）。透過 `errors.Is` 的強型別攔截將資源不足等業務錯誤 (HTTP 400) 與服務異常 (HTTP 500) 精準隔離，在 100 VU 的滿載壓測中實現 100% SLA，並確保核心訂單流 (Order-to-WS) 的端到端 P95 延遲小於 30ms。

**資料量與觀測力維度的加分論述**：
> - **系統韌性保護**：在 Spike 測試中面對 10 倍超載流量，系統藉助 Gateway 限流器與精準的 DB 連線池配置 (MinOpen/MaxOpen)，優雅且毫不留情地回傳 429 拒絕連線，以犧牲部份新請求為代價，保證了後方資料庫與既有連線 0% OOM、0% 崩潰率。
> - **動態壓測調配**：為了徹底打出系統真極限，壓測腳本內建「動態充值」協定，當 VU 把測試資金打光時自動展延，解決傳統壓測經常因為假性資料瓶頸而提早結束的痛點。

**關鍵數據**：
| 測試類型 | 場景 | 關鍵結果 |
|:---|:---|:---|
| Market Storm | 讀寫隔離：千人 WS 接收時狂下單 | WebSocket 的推播 CPU 開銷不影響 Order 寫入，P95 < 50ms |
| E2E Latency | 訊息溯源延遲 (HTTP -> DB -> Kafka -> WS) | P95 整條鏈路延遲穩定控在 30 毫秒內 |
| Spike Test | 突發 10x 流量衝擊 | 429 優雅降級, DB 連線池不被打穿, 0% 崩潰率 |
| Batch Ingestion | 造市商與套利機器人瞬時丟單 | 使用 CopyFrom，單節點創造破 40k+ TPS 的寫入量能 |

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
> 利用 Go Goroutine 輕量級特性設計無鎖 WebSocket 廣播機制，並抽離為獨立服務。實作 **廣播合流 (Conflation)** 與 **主動背壓 (Backpressure) 管理**，在行情暴增時精準追蹤訊息丟棄率 (Dropped Rate)，確保單一慢速連線不會拖垮整體扇出效能（單節點 2,000+ 長連線，60,000 msg/s）。

---

## 5. 雲原生部署與 DevOps (Cloud Native & CI/CD)

**履歷條目**：
> 建置基於 GitHub Actions 與 GitOps 的 CI/CD 工作流，為四個微服務（Gateway, Order, Matching, Market Data）實現自動化建置、推送至 GHCR 並透過 ArgoCD 同步部署至 K3s 叢集。

---

## 6. 記憶體內撮合引擎 (In-Memory Matching Engine)

**履歷條目**：
> 開發支援價格/時間優先 (Price-Time Priority) 的高效能記憶體撮合引擎，完整支援市價/限價單及部分成交邏輯。啟動時從 DB 恢復活動訂單至記憶體，並預熱 Redis OrderBook 快取，確保冷啟動後首筆市價單即可正確估算資金。
