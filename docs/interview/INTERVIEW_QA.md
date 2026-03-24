# 面試深度問答 (Interview Q&A)

本文件收錄資深後端工程師面試中，針對本撮合系統專案最可能被問的問題，以及建議的 STAR 應答劇本。

> 核心主軸：**壓力測試 → 發現瓶頸 → 架構演進 → 資料一致性 → 資料量維度**

---

## 一、系統架構與分散式設計

### Q1：為什麼從單體轉換成 Kafka 事件驅動架構？

> **情境**：Phase 1 單體架構中，API 收到下單後直接同步撮合並寫入 DB。
>
> **痛點**：用 k6 壓測時發現，只要流量一上來（Spike），PostgreSQL 的連線池和 I/O 馬上成為瓶頸。大量 Row-level lock 在單機硬碟 I/O 上產生排隊，API 回應時間暴增甚至 Timeout。
>
> **行動**：將系統拆分為 Gateway / Order Service / Matching Engine / Market Data Service 四個微服務，並在「下單」與「撮合」之間插入 Kafka 作為緩衝。
>
> **結果**：API Server 現在只負責 TX1（鎖資金 + 建單）後就將事件推入 Kafka 回傳，系統吞吐量大幅提升。撮合引擎與資料庫可以依照自己的步調消化 Queue 中的任務，達到**削峰填谷**。在相同的測試環境下，架構調整後 Spike 測試不再出現 500 錯誤。

### Q2：Kafka 的事件順序性如何保證？Rebalance 時怎麼辦？

> **回答**：同一交易對 (Symbol) 的訂單與撤單事件透過 **Partition Key = Symbol** 保證嚴格有序。在 Rebalance 時：
> 1. Consumer 先停止處理新訊息
> 2. 已處理但未 Commit 的 Offset 會被重新派發
> 3. 透過結算層的**冪等性檢查**（基於 Trade ID 的 Unique Key）確保重複派發不會造成重複扣款
>
> 所以順序性由 Partition Key 保障，重派的安全性由冪等性保障。

### Q3：如果明天上線要扛 5,000 TPS，你怎麼擴展？

> **回答**：因為系統一開始就設計為事件驅動微服務，所以很容易水平擴展：
> 1. **Gateway & API**：Stateless，直接在 K8s 上起多個 Pod，前面掛 Load Balancer
> 2. **行情推播**：Market Data Service 也是 Stateless，可獨立擴容
> 3. **撮合引擎**：可按 Symbol 分片，每個引擎負責不同的交易對
> 4. **資料庫（最難）**：短期升級 IOPS（如 Aurora），中長期導入 Database Sharding，按 UserID 切分帳戶表，把鎖的競爭分散到不同實體

---

## 二、壓力測試與效能瓶頸

### Q4：你測出的 TPS 大概多少？這算快還是慢？

> **回答**：「在單機 Docker 環境下，下單 TPS 極限大約 500 左右。但這**不是** Go 程式或撮合引擎的極限（記憶體撮合可以輕鬆破萬）。真正的瓶頸卡在 **PostgreSQL 的寫入事務**。每筆下單與結算都需要 `鎖定資金 → 扣款 → 寫入訂單`，大量 Row-level lock 在單機硬碟 I/O 上排隊。」
>
> **重點**：面試官不是要聽絕對數字，而是要聽你**找瓶頸的方法論**和**解決瓶頸的架構決策**。

### Q5：本地壓測的數據能代表線上嗎？

> **回答**（這是展現資深視野的絕佳機會）：
>
> 「本地端的壓測數據（例如 5,000 QPS）放到線上環境並**沒有**絕對參考價值，因為雲端的網路拓撲、Disk I/O 限制、Load Balancer 行為都不同。但透過 k6 在本地跑 Load 和 Spike 測試，最大的價值是：
> 1. **對比架構重構前後的相對成長**
> 2. **提早發現系統最脆弱的環節**（是 DB 鎖死還是 CPU 滿載）
> 3. **驗證架構設計的正確性**——引入 Kafka 確實能保護後端核心不被擊垮
>
> 如果部署到雲端（如 AWS ECS），我會重新定義環境規格並再次測試，產出具有環境標註的測試報告。」

### Q6：壓測時「資料量大小」會影響瓶頸嗎？

> **回答**（主動拋出此觀點會讓你脫穎而出）：
>
> 「**一定會，而且瓶頸會完全轉移。**
>
> | 場景 | 瓶頸位置 | 原因 |
> |:---|:---|:---|
> | 大流量 + 小資料量 | API Server CPU / 框架負載 | DB 資料少，全在 Buffer Pool，回應極快 |
> | 大流量 + 大資料量 | PostgreSQL Disk I/O / B-Tree 索引深度 | 資料量大，Buffer Pool miss 增加，Page Fault 頻繁 |
>
> 所以我在壓測時，不只跑空資料庫，還會預先塞入百萬筆歷史訂單（M 級資料量）再跑相同的 k6 腳本。這樣才能觀察到真實場景下的退化程度。」
>
> **延伸**：「這也是我規劃 CQRS 架構的原因——不能讓使用者的歷史訂單查詢（Read）去拖累撮合引擎的結算寫入（Write）。透過 Kafka 事件非同步更新到 Redis 或讀取專用 DB，才能解決大資料 + 大流量的雙重挑戰。」

---

## 三、資料一致性與交易安全

### Q7：如何證明幾千個 Request 同時砸下來時，帳不會算錯？

> **回答**：「我設置了三道防線：
> 1. **事前預扣**：下單當下即在 DB 內將資金移入 `locked` 欄位，杜絕超額下單
> 2. **序列化防死鎖**：結算時將所有涉及的 Order ID 排序後依序取得排他鎖 (`FOR UPDATE`)，根除死鎖與 Lost Update
> 3. **冪等性防禦**：基於 Trade ID 的 Unique Key，確保 Kafka 重複投遞不會重複扣款
>
> 最終驗證：每輪壓測後執行 Correctness Audit SQL，確認 `SUM(balance + locked)` 壓測前後**完全一致，一毛不差**。」

### Q8：撮合成功但結算時 DB 寫入失敗，怎麼辦？

> **回答**：
> 1. Kafka Consumer 處理結算事件時，如果 DB 寫入失敗，**不 Commit Offset**
> 2. Kafka 會在 Timeout 後重新派發同一筆結算事件
> 3. Consumer 重新收到事件後，冪等性檢查會先查 Trade ID 是否已存在
> 4. 如果已存在就跳過，如果不存在就正常執行 TX2 結算
>
> 所以系統的補償機制是：**依賴 Kafka 的 At-least-once 投遞 + DB 層的冪等性防禦**，而非自己寫複雜的 Saga 補償。

### Q9：為什麼用 `shopspring/decimal` 而不用 `float64`？

> **回答**：「浮點數在金融系統中是災難。`0.1 + 0.2` 在 float64 下不等於 `0.3`，長期累積下來資金會產生不可追溯的誤差。`shopspring/decimal` 使用精確的十進位運算，確保每一分錢都精確到小數點後 8 位。在交易系統中，一個 0.00000001 的累積誤差在百萬筆交易後就會變成可觀的金額。」

---

## 四、核心撮合引擎

### Q10：Order Book 用什麼資料結構？為什麼？

> **回答**：「使用 Red-Black Tree（或 Skip List）搭配 Hash Map。
> - **Tree** 用於維護價格層級的排序（買方最高價先撮、賣方最低價先撮），插入/刪除 O(log n)
> - **Hash Map** 用於 O(1) 查找特定 Order ID（取消訂單時直接定位）
>
> 在大資料量場景下（某交易對累積數十萬筆掛單），Tree 的 O(log n) 不會因節點過多而嚴重劣化，而純 Array 的 O(n) 搜尋則會崩潰。」

### Q11：市價單在深度不足時怎麼處理？

> **回答**：「市價買單需要估算所需資金。在微服務架構下，order-service 本身沒有完整的 Order Book（那在 matching-engine 裡），所以會先去 Redis 讀取最新的 OrderBook Snapshot 來估算。如果 asks 深度不足以填滿整筆市價單，系統有兩種策略：
> 1. 拒絕下單並回傳 `insufficient liquidity`
> 2. 允許部分成交（Partial Fill），鎖定已知深度的資金量
>
> matching-engine 啟動時會預熱 Redis 快取，避免冷啟動時 order-service 讀到空的 snapshot。」

---

## 五、高可用與防禦性設計

### Q12：限流策略怎麼實作的？

> **回答**：「使用 Redis-backed Token Bucket 演算法。每個 API Key 在 Redis 中維護一個 Token 計數器：
> - 每次請求消耗一個 Token
> - Token 按固定速率補充
> - 當 Token 為 0 時回傳 `429 Too Many Requests`
>
> 在 Spike 測試中（800 VU 瞬間湧入），限流器精準攔截了 85% 的超額流量，確保後端核心服務 0% 崩潰。限流邏輯放在 Gateway 層，不讓惡意或過量流量穿透到 Order Service。」

### Q13：WebSocket 為什麼要獨立成 Market Data Service？

> **回答**：「因為 WebSocket 是**長連線、記憶體密集型**；交易結算是 **DB / CPU / I/O 密集型**。如果放在一起：
> - 市場大幅波動時的推播暴增會直接把交易核心一起拖垮
> - WebSocket 連線數增加會佔用大量記憶體，擠壓結算事務的資源
>
> 拆開後兩者可以獨立擴容，互不影響。Market Data Service 只消費 Kafka 事件並轉推 WebSocket，完全不碰 DB 和核心撮合。」

---

## 六、CI/CD 與部署

### Q14：容器化策略與滾動更新？

> **回答**：「每個微服務有獨立的 Dockerfile，使用 Multi-stage build 減小 Image 大小。CI/CD 流程：
> 1. Push 到 `main` 觸發 GitHub Actions
> 2. 執行測試 → Build 4 個服務 Image → Push 至 GHCR
> 3. 自動更新 GitOps Repo 的 Image Tag（使用 `sha-<commit>` 避免 `latest` 追蹤問題）
> 4. ArgoCD 偵測變更後自動 Sync 到 K3s
>
> 滾動更新靠 K8s 的 Rolling Update Strategy + Readiness Probe，確保新版本通過 `/health` 檢查後才接收流量。」

---

## 面試心法

1. **不要背數字，展示方法論**：面試官在意的是你如何找到瓶頸、如何驗證架構決策，而非「我的系統能跑 10 萬 QPS」
2. **主動提「資料量」維度**：大多數候選人只會說「用 Kafka 擋流量」，你額外提出「空資料庫 vs 百萬筆訂單的瓶頸轉移」會讓你脫穎而出
3. **誠實定義測試環境**：「這是在本地 Docker 環境測的，不代表線上」展現的是實事求是的工程師精神
4. **用 Correctness Audit 做結尾**：不論面試官問什麼效能問題，都有意識地帶到「但比 TPS 更重要的是帳不能算錯」，展現金融系統的嚴謹度
