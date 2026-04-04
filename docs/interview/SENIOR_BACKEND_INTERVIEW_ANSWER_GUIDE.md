# 資深後端面試回答參考：加密貨幣交易所後端

> 本文件對應 `SENIOR_BACKEND_INTERVIEW_QUESTIONS.md`，以面試者視角整理示範回答。
>
> 重點不是把系統講成完美，而是要能清楚說明：**我現在做到哪裡、為什麼這樣設計、我如何驗證、目前還缺什麼**。

## 1. 作答原則

| 原則 | 建議說法 |
| :--- | :--- |
| 先講目標 | 先定義這題在解什麼問題，例如一致性、吞吐量、故障隔離或可營運性 |
| 再講設計 | 說明採用的設計與關鍵 trade-off，不要只背名詞 |
| 一定要給證據 | 盡量提到程式實作、測試、壓測、audit SQL、metrics 或 runbook |
| 誠實交代缺口 | 對於 tracing、DLQ、完整 auth、soak automation 等未完成項，直接說清楚 |
| 不過度宣稱 | 不要把 baseline 壓測說成 production-grade capacity proof |

## 2. 90 秒開場版

如果面試官要我先介紹這個專案，我會這樣回答：

> 這是一套以 Go 實作的加密貨幣交易所後端，我刻意把它做成事件驅動微服務，而不是只停留在單體 CRUD。核心路徑是 `gateway -> order-service -> Kafka -> matching-engine -> settlement -> market-data-service`。在一致性上，我把下單拆成兩段：TX1 負責鎖資金與建單，TX2 負責撮合後的結算；兩段之間用 Kafka 解耦，並用 Transactional Outbox 避免 TX1 成功但事件遺失的雙寫風險。撮合引擎是記憶體內的 order book，強調價格優先、時間優先，並用相同 `symbol` 作為 partition key 保證同一交易對事件嚴格有序。高可用方面，我做了基於 PostgreSQL 租約的 leader election，搭配 fencing token 防腦裂。驗證上除了單元、整合、race detector，也有 k6 baseline 壓測與 correctness audit SQL。不過我也會誠實說，目前還有幾個 production gap，像是完整的 DLQ / replay、分散式 tracing、Go runtime metrics、以及自動化 chaos / soak test，這些是我下一階段會優先補的能力。

## 3. 對應題庫第 2 節：系統全貌與服務切分

這一節可覆蓋題庫 `2.1` 到 `2.8`。

### 核心回答

我拆成四個服務，不是為了追求微服務而微服務，而是因為交易系統的壓力型態本來就不同。`gateway` 負責入口流量、防護與轉發；`order-service` 專注在強一致的交易前段，也就是鎖資金、建單、寫 outbox；`matching-engine` 專注在單執行序語意下的撮合與事件發布；`market-data-service` 專注在高 fan-out 的 WebSocket 推播。這樣拆的核心目標是把高延遲、可非同步的工作從 API 主路徑移開，讓下單 API 不必等待完整撮合與推播才回應。

如果面試官問我哪裡是強一致、哪裡是最終一致，我會明講：資金鎖定、訂單建立、訂單取消、成交結算這些會影響帳務的操作，必須是強一致；order book 快照、WebSocket 推播、部分查詢快取則接受最終一致。這樣做是典型交易系統的切法，因為 correctness 必須先於體驗，但體驗也不能拖垮 correctness。

如果今天流量很小，我不會堅持一開始就拆這麼細。我會說這個專案保留了 monolith / fallback 路徑，本身就反映出我不是教條式地推微服務。對小流量場景，我會優先保留 order-service 與 matching 的核心邏輯，視團隊規模與運維成本決定是否合併 gateway 與 market-data-service。

### 面試時可補充的追問答案

- **為什麼從單體走向事件驅動**：我遇到的主要瓶頸不是純 CPU，而是 spike 流量下 PostgreSQL row lock、連線池與 I/O 壓力一起上來，導致 API latency 飆高。把下單與撮合切開後，Kafka 扮演削峰填谷的緩衝層。
- **目前最脆弱的節點**：我會說最值得小心的是資料庫與單一熱門 symbol 的序列化瓶頸。資料庫是 correctness 根基，而熱門交易對會把單一 partition 與單一引擎實例壓到極限。
- **SLA / SLO 怎麼想**：我會把 API 成功率、P95 latency、order throughput、consumer lag、WS loss rate、correctness audit 異常數一起看，而不是只看 TPS。
- **服務切分依據**：這不是只看團隊分工，而是依照資源型態切割。撮合需要順序與低延遲，WS 需要大量連線與背壓隔離，兩者放一起很容易互相拖累。

## 4. 對應題庫第 3 節：交易領域模型與核心流程

這一節可覆蓋題庫 `3.1` 到 `3.8`。

### 核心回答

目前核心支援限價單與市價單，狀態上以 `NEW`、`PARTIALLY_FILLED`、`FILLED`、`CANCELED` 為主。我的設計原則是：任何會影響資產守恆與可審計性的狀態轉移，都必須能回推到一個具體的交易事件與資料庫寫入。這也是為什麼我很重視「成交紀錄、訂單狀態、資金變動」三者必須能互相對帳，而不是只讓 API 看起來成功。

撮合語意上，我遵守價格優先、時間優先，成交價格採 maker price。這一點在測試裡有覆蓋。STP 部分，目前實作是 `Cancel Newest`，也就是新進來的同帳號訂單一旦撞到自己的對手單，會把剩餘量直接歸零，不讓它形成 crossed book。這樣的好處是規則簡單、行為明確，但如果未來產品要求不同 STP policy，例如 cancel oldest 或 decrease and cancel，我會把它抽成策略層而不是寫死在引擎裡。

### 面試時可補充的追問答案

- **部分成交怎麼處理**：我會先更新 maker 與 taker 的 `filled_quantity`，再依最終結果判斷 `PARTIALLY_FILLED` 或 `FILLED`。帳務上則根據成交量、成交價格與剩餘保證金計算 unlock / balance update。
- **市價單與限價單差異**：限價單重點是價格條件與剩餘量是否入 book；市價單重點是流動性足不足，以及預扣資金與退款計算是否正確。
- **為什麼用 UUID v7**：主要是希望保有全域唯一性，同時帶時間排序特性，減少 B-Tree 索引碎片化。這比純隨機 UUID 更適合高寫入表。
- **correctness invariant**：我最重視三件事：總資產守恆、已完成訂單的 `filled_quantity` 必須對得上 trades、以及不能存在 locked funds 卻沒有對應 open order 的帳戶。

## 5. 對應題庫第 4 節：下單、資金鎖定與一致性設計

這一節可覆蓋題庫 `4.1` 到 `4.9`。

### 核心回答

我把下單拆成 TX1 與 TX2，是因為這兩段工作的延遲與一致性需求不同。TX1 負責資金鎖定與建單，這一段一定要強一致，因為它決定系統是否接受這張單。TX2 負責撮合後的帳務結算、成交寫入與狀態更新，這一段可以非同步，但仍然必須原子執行。這樣拆的結果是 API 可以在 TX1 完成後快速返回，再讓 Kafka 與 consumer 去消化後續工作。

設計上我很在意順序：一定是先鎖資金、再建單、再寫出box / 發事件。順序如果顛倒，就可能出現「事件已送出但 DB 沒有這張單」或「訂單建好了但資金其實不足」這種金融級錯誤。TX2 內部則把 maker 與 taker 的訂單先統一排序，再 `SELECT ... FOR UPDATE` 逐筆取鎖，避免不同 goroutine 在不同順序拿鎖造成 deadlock。

### 面試時可補充的追問答案

- **TX1 成功但 Kafka 發送失敗怎麼辦**：在有 outbox 的模式下，TX1 內會同步寫入 outbox message，由背景 worker 可靠地重送到 Kafka，所以訂單不會遺失。沒有 outbox 的 fallback 模式則承認存在雙寫風險，這也是我會明說只適合作為相容路徑、而不是最終形態的原因。
- **撮合成功但 TX2 失敗怎麼辦**：我依賴 Kafka 的 at-least-once 與 DB 冪等保護，而不是自己做複雜 Saga。consumer 不 commit，事件會重派，結算層再用 trade ID 與 order 狀態做去重。
- **如何避免 double spend**：本質上靠的是先鎖資金、交易內排他鎖、固定取鎖順序與聚合後再做帳務更新。不是靠業務層 if 判斷而已。
- **怎麼證明資產守恆**：我不只口頭說，而是會跑 correctness audit SQL，驗證 `SUM(balance + locked)` 壓測前後一致、沒有孤兒 locked funds、沒有 filled order 對不上 trade 的情況。

## 6. 對應題庫第 5 節：Kafka、事件驅動與 Outbox

這一節可覆蓋題庫 `5.1` 到 `5.12`。

### 核心回答

我把事件流拆成幾個責任清楚的 topic，例如 `exchange.orders` 負責下單與撤單命令，`exchange.settlements` 負責撮合後的結算請求，`exchange.trades`、`exchange.orderbook`、`exchange.order_updates` 則給下游訂閱與推播。最關鍵的設計是：同一交易對的下單與撤單都走同一個 topic，partition key 用 `symbol`。這樣 matching-engine 才能保證同一交易對事件嚴格有序，不會出現先撤後下、或重複撮合同一張單的語義錯亂。

我選擇 at-least-once 而不是 exactly-once，是因為對這個專案階段來說，靠 DB 冪等與唯一鍵就能把複雜度控制在合理範圍內。Transactional Outbox 則是關鍵補強，因為它解的是最經典的雙寫問題：DB commit 成功，但 Kafka publish 失敗。把 outbox 訊息寫進同一個 transaction，才有辦法保證「訂單被接受」與「事件最終一定可被送出」這兩件事不分裂。

### 面試時可補充的追問答案

- **Outbox worker 為什麼用 `SKIP LOCKED`**：這是為了讓多 worker 可以安全並行批次領取待發訊息，不會互相阻塞或重複處理同一批 row。
- **未知 event type 為什麼直接 commit**：這是消費端韌性的選擇，避免整個 consumer 卡死在壞訊息上。不過風險是壞事件會被跳過，所以正式環境我會補 DLQ 或 quarantine 機制。
- **consumer 發布 settlement 後 crash 會怎樣**：這題我會坦白說，系統不是靠「永不重複」保證安全，而是靠 idempotent settlement 保證「重複也不會重複扣款」。
- **事件 schema 演進**：我會優先採向後相容新增欄位，避免直接改語意或刪欄位。若真的要破壞性變更，就用 versioned event 而不是硬改原 schema。
- **目前有沒有 DLQ**：我會誠實回答，現在重點放在核心正確性，DLQ / replay 還是下一階段要補的營運能力。

## 7. 對應題庫第 6 節：撮合引擎、順序性與高可用

這一節可覆蓋題庫 `6.1` 到 `6.10`。

### 核心回答

目前撮合引擎是記憶體內的 order book，採用 slice 加 stable sort 維護 bids 與 asks。這代表現階段我的設計優先順序是 correctness、可理解性與測試可控，而不是一開始就做成極致高性能的 price-level tree。價格優先由排序保證，時間優先則仰賴 stable sort 保留同價位的插入順序。這在專案目前階段是合理的，因為我先要把業務正確性與併發風險壓住。

高可用方面，我做的是以 PostgreSQL 為基礎的 leader election。具體來說，會在 `partition_leader_locks` 表上用 `upsert + WHERE expires_at < now` 競選租約，並在取得鎖時讓 fencing token 單調遞增。之後 leader 在續租時必須同時符合 `leader_id` 與 `fencing_token`，如果續租失敗，就代表自己已經被新 leader 取代，必須立刻退回 standby。這樣就能防止舊 leader 在網路抖動後復活，繼續做過期寫入。

### 面試時可補充的追問答案

- **冷啟動為什麼要 restore active orders**：因為 matching-engine 的狀態在記憶體，服務重啟後一定要從 DB 把仍然有效的限價單灌回來。市價單不應進 order book，所以 restore 時會忽略非限價單。
- **STP 規則是什麼**：目前是 `Cancel Newest`，當新單遇到同 user 的最佳對手單時，直接把新單剩餘量清零，避免自成交與 crossed book。
- **這個 order book 的複雜度如何**：我會誠實說，目前因為用 slice 加 sort，插入不是最優，未來若要支撐更高吞吐量，會改為 price level map 加 heap / tree 結構。
- **熱門 symbol 怎麼擴展**：現在的序列化粒度是 symbol。若熱點集中在單一 symbol，橫向擴展不會自動解決這個問題，必須重新思考 shard 規則或更細的撮合拓樸。

## 8. 對應題庫第 7 節：資料庫、鎖與併發控制

這一節可覆蓋題庫 `7.1` 到 `7.9`。

### 核心回答

這個專案對資料庫的依賴非常深，因為 correctness 很大一部分是靠 PostgreSQL 的交易語意保障。關鍵工具包括 row-level lock、`SELECT FOR UPDATE`、唯一鍵、以及在交易內固定順序取得資源。舉例來說，結算時我會先收集所有 maker 與 taker 的 order ID，做固定排序，再逐筆取鎖。這不是為了好看，而是因為不同 goroutine 若用不同順序拿鎖，非常容易造成 deadlock。

我對「一次正確的結算交易」的定義是：訂單狀態更新、成交紀錄寫入、資金解鎖與餘額變更必須在同一個 transaction 裡成功或失敗，不能出現只完成其中一部分。這也是為什麼我把 TX2 包成一個原子事務，而不是拆成多段獨立更新。

### 面試時可補充的追問答案

- **重複 settlement event 怎麼處理**：consumer 外層會先查 trade 是否存在，TX 內再做一次冪等檢查，避免 TOCTOU。這樣即使事件重派，也不會重複結算。
- **哪些索引是 correctness 關鍵**：像 trade ID 的唯一性就不是只為了查詢快，而是直接參與去重與避免重複扣款。
- **schema migration 風險**：order-service 啟動時自動跑 schema 在單機開發方便，但多實例環境如果沒有更成熟的 migration discipline，會有啟動競爭與變更順序風險。
- **DB 變慢時哪裡先爆**：我會先看 TX1 latency、DB active connections、outbox backlog、settlement lag。通常不是單一 query 慢，而是整條鏈路排隊變長。

## 9. 對應題庫第 8 節：Redis、快取、限流與冪等

這一節可覆蓋題庫 `8.1` 到 `8.8`。

### 核心回答

Redis 在這個專案裡不是單純快取，而是被用在三種不同角色：第一是 order book snapshot 快取，讓 order-service 在沒有完整書本狀態的情況下仍能估算市價買單所需資金；第二是 gateway 的分散式限流；第三是跨實例一致的 idempotency store。這三種用途的容錯策略其實不一樣，不能混為一談。

限流部分，我現在的核心演算法是 Redis-backed token bucket，透過 Lua script 保證更新 token 與 timestamp 的原子性。文件若提到 sliding window，我在面試時會主動修正說法，因為實作上比較接近 token bucket。這類細節如果我自己先講清楚，面試官通常反而會覺得我有真的讀過自己的程式，而不是只會背文件。

### 面試時可補充的追問答案

- **市價單估價依賴 Redis 會不會不準**：會有快照新鮮度問題，所以我把它定位為估算，不是最終成交保證。真正的成交結果仍由 matching-engine 決定，並在 TX2 結算時修正剩餘保證金與退款。
- **為什麼要 warm up snapshot**：matching-engine 啟動後先把目前書本快照寫到 Redis，避免 order-service 在剛啟動時讀到空快取，導致市價單估算完全失真。
- **Redis 掛掉時退回 memory 模式有什麼問題**：單機可用，但多實例下限流與 idempotency 不再全域一致，這是我會明說的風險。
- **Idempotency-Key TTL 怎麼想**：太短可能擋不住客戶端重試，太長則會讓合法重送被誤判重複，也會增加儲存成本。我會依 API 性質與重試窗口決定 TTL。

## 10. 對應題庫第 9 節：API Gateway、WebSocket 與安全

這一節可覆蓋題庫 `9.1` 到 `9.8`。

### 核心回答

gateway 的定位不是只有反向代理，而是系統的安全與流量整形邊界。它負責限流、冪等、路由分流，目標是不要讓異常流量直接打穿 order-service。WebSocket 之所以獨立成 market-data-service，是因為長連線、高 fan-out、慢客戶端背壓，和交易核心的資源需求完全不同。把推播拆出去，可以避免某一批慢連線拖垮核心撮合與帳務路徑。

目前 WebSocket 層已有 per-client send buffer、broadcast queue，以及 buffer 滿時主動剔除慢客戶端的策略。這代表我有處理背壓，而不是單純把訊息直接寫 socket。不過我也會誠實說，目前授權與私有事件隔離仍偏簡化，開發模式下 `CheckOrigin` 也較寬鬆，所以正式環境一定要再補完整 auth 與 origin policy。

### 面試時可補充的追問答案

- **public / private API 怎麼分**：查詢行情、order book 等公開資料可放 public；訂單、帳戶、模擬器控制這類涉及身分與資產的操作則必須 private。
- **JWT 現況怎麼說**：我不會裝作完整做好。比較好的說法是：目前路由與身份欄位已有邊界雛形，但正式版會改成完整 JWT / session 驗證，而不是只靠 header 或 query 參數。
- **為什麼推 snapshot 而不是 diff**：snapshot 比較簡單、恢復性高，客戶端重連也容易同步。不過流量更大時，diff 會更省頻寬，需要再做版本號與重放設計。
- **slow consumer 怎麼處理**：目前是 non-blocking broadcast，加上 client buffer 滿就剔除，確保推播不會反向阻塞核心流程。

## 11. 對應題庫第 10 節：可觀測性、SRE 與故障處理

這一節可覆蓋題庫 `10.1` 到 `10.8`。

### 核心回答

我在這個專案裡不是只看 CPU 與 memory，而是刻意用黃金信號去看服務健康，因為交易系統真正重要的是 latency、traffic、errors、saturation 這四件事如何一起變化。像 outbox backlog、consumer lag、leader renewal、WS broadcast dropped count，都比單純 CPU 更能直接反映系統是否還在正確工作。

如果真的遇到故障，我的判斷順序通常是：先看 error rate 與 latency 是否同時異常，再對照 Kafka event throughput、outbox pending、DB latency、Redis hit/miss、WS loss rate。這樣比較能分辨是入口流量問題、資料庫壅塞、事件鏈路卡住，還是推播層出問題。

### 面試時可補充的追問答案

- **最重要的告警**：我通常會優先盯下單失敗率、correctness 異常訊號、以及事件積壓。因為交易系統最不能接受的是錯帳與靜默失敗。
- **恢復的定義**：不是 health endpoint 回 200 就算恢復，而是故障後延遲回到基線、lag 清掉、且 correctness audit 沒出現異常。
- **目前觀測缺口**：我會主動說現在仍缺 Go runtime metrics、完整 tracing、DB / Redis exporter 與更成熟的 lag 監控，這些是營運成熟度上的缺口。
- **跨服務定位能力**：目前以 metrics 與 logs 為主，若要再往上提升，我會補 request / event correlation ID 與 distributed tracing。

## 12. 對應題庫第 11 節：測試策略、Correctness 與壓測證據

這一節可覆蓋題庫 `11.1` 到 `11.10`。

### 核心回答

我對這個專案的測試觀念是「不能只測功能，要測 correctness 與退化行為」。單元測試負責驗證撮合規則、狀態轉移與計算邏輯；integration / e2e 負責驗證真實 DB、transaction 與 event flow；race detector 用來抓資料競態；k6 baseline 則驗證在有壓力時 API 與 WS 是否出現明顯崩潰或退化。這幾層測試不是互相替代，而是分工不同。

我很重視 correctness audit，因為交易系統不能只說 TPS 漂亮。每輪壓測後，我會檢查總資產是否守恆、是否有 locked funds 沒對應 open order、是否有 stuck orders、以及 filled order 的成交量能不能對上 trades。這幾個 SQL 比單純看 P95 更接近交易系統真正該被驗證的東西。

### 面試時可補充的追問答案

- **為什麼 baseline 壓測不等於 production proof**：因為 baseline 只能說基本負載下沒有明顯崩潰，不能直接證明 resilience、chaos recovery、large dataset behavior 或 24h soak stability。
- **本地與 ECS 差異**：本地適合抓 correctness bug 與明顯退化，ECS 才比較能回答容量、網路、跨實例與雲端基礎設施下的真實行為。
- **spike 測試遇到很多 429 怎麼看**：如果 429 來自有意義的 gateway 限流，而且 5xx 很低，我會解讀成保護機制生效，而不是系統崩潰。
- **目前缺口怎麼說**：自動化 correctness audit script、chaos tooling、完整 soak suite 都還沒完全成形，這些我會明確說成下一階段目標。

## 13. 對應題庫第 12 節：部署、雲端與營運成熟度

這一節可覆蓋題庫 `12.1` 到 `12.10`。

### 核心回答

我選 ECS、Terraform、ecspresso 這組合，是因為它對個人或小團隊來說，在複雜度與雲端原生能力之間有不錯的平衡。Terraform 負責基礎設施可重現，ECS 提供容器調度與 ALB 整合，ecspresso 則讓部署流程比較簡潔。這比一開始就上 Kubernetes 更容易把重點放回業務 correctness，而不是把時間全部花在平台工程上。

不過我不會把現在這個狀態說成完全 production-ready。像 monolith 路徑仍然保留，就代表系統還在演進期；health check 目前偏基礎，也還沒把 readiness、lag、依賴服務狀態整合成更完整的就緒訊號。真正要上 production，我會優先補 secrets management、回滾策略驗證、schema migration discipline，以及 deployment 期間的 leader 切換演練。

### 面試時可補充的追問答案

- **保留 monolith 路徑的看法**：我會說這是演進策略，不是搖擺不定。它讓我能做相容與 fallback，但長期一定要收斂，不然維運成本會升高。
- **Kubernetes 何時值得**：當服務數量、團隊規模、流量型態與平台需求上升時，K8s 的抽象才比較值得付出成本。不是所有專案都應該一開始就上。
- **正式上 production 還缺什麼**：我通常會回答三項：更成熟的 auth / secrets、更完整的 observability / tracing / DLQ、以及自動化 chaos / soak / correctness 驗證。
- **成本怎麼看**：對這類系統，資料庫、訊息系統與長連線服務往往比單純 API container 更容易成為主要成本來源。

## 14. 對應題庫第 13 節：高壓追問與反思題

這一節可覆蓋題庫 `13.1` 到 `13.10`。

### 核心回答

這一節最重要的不是把自己講得很厲害，而是展現技術判斷力。我會主動區分三件事：第一，哪些能力我已經有程式與測試證據支撐；第二，哪些能力目前只是正確方向但證據還不夠；第三，如果給我三個月，我會先補什麼。這樣的回答通常比一直強調「我做了很多」更有說服力。

如果要我自我批判，我會說目前最有把握的是核心交易一致性、撮合語意、事件有序與冪等這一層，因為這些在程式邏輯與測試上都有具體落地。比較想重構或補強的，則是更高吞吐量的 order book 資料結構、更成熟的私有事件授權、完整 replay / DLQ、以及更系統化的營運驗證工具鏈。

### 面試時可補充的追問答案

- **如果交易量成長 10 倍**：我第一個懷疑的是單一熱門 symbol 的 partition 與 matching engine 瓶頸，其次是 DB 寫入壓力與 WS fan-out。
- **如果面試官質疑是 demo**：我會承認目前還沒補完所有 production 能力，但會強調這不是只會做 REST API 的 demo，因為它已經處理了交易系統最難的一致性、冪等、順序與故障恢復問題。
- **如果被說過度設計**：我會回到 trade-off。本專案是刻意練習交易系統的高風險問題，所以我接受一定程度的設計成本；但我也保留 fallback / monolith 路徑，代表我不是盲目追求複雜度。
- **三個月優先順序**：我會先補 observability 與 tracing，再補 DLQ / replay 與 migration discipline，最後補 chaos / soak 自動化與更高性能的 order book 結構。

## 15. 高分回答模板

如果臨場忘詞，可以用下面這個模板回答大多數系統設計題：

1. 先定義問題：這題本質是在解一致性、吞吐量、故障隔離，還是營運可觀測性。
2. 說我現在的做法：我目前在專案裡的具體實作是什麼，不要只講理論。
3. 說我為什麼這樣選：講出成本、收益與取捨。
4. 說我怎麼驗證：提測試、壓測、audit、metrics、runbook。
5. 說我知道的缺口：指出目前還沒做完的地方與下一步。

## 16. 建議優先背熟的 10 題答案

如果時間有限，我會優先把以下 10 題講熟：

1. 為什麼要拆四個服務，以及資料流怎麼走。
2. TX1 / TX2 為什麼分開，強一致邊界在哪裡。
3. Transactional Outbox 解決什麼問題。
4. Kafka 為什麼用 `symbol` 當 partition key。
5. 撮合引擎如何保證價格優先與時間優先。
6. STP 現在的策略是什麼，為什麼這樣選。
7. Leader election 與 fencing token 如何防腦裂。
8. Correctness audit SQL 驗證什麼。
9. Redis 快取與市價單資金估算的風險邊界。
10. 我認為目前距離 production-ready 還缺哪三件事。
