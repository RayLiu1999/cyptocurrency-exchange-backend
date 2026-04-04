# 資深後端面試題庫：加密貨幣交易所後端

> 這份題庫以目前專案架構為基礎，從資深後端面試官的角度設計，重點放在架構取捨、一致性、故障恢復、可觀測性、測試證據與可營運性。

## 1. 面試判斷重點

| 面向 | 面試官想確認什麼 |
| :--- | :--- |
| 架構理解 | 是否真的理解事件驅動微服務，而不是只會背名詞 |
| 一致性 | 是否能清楚說明 TX1、TX2、Outbox、冪等與重試的邊界 |
| 撮合與交易 | 是否理解交易所核心 domain 與 correctness invariant |
| 故障處理 | 是否知道 Kafka、Redis、PostgreSQL、WS 失效時怎麼退化 |
| 效能與容量 | 是否能從資料與指標說明瓶頸，而不是只會講吞吐量 |
| 測試與驗證 | 是否有 unit、integration、race、壓測、audit 的證據鏈 |

## 2. 系統全貌與服務切分

1. 你先用 3 到 5 分鐘講完整個交易流程，從前端送出下單請求到成交推播回前端，中間經過哪些元件？
2. 為什麼要拆成 gateway、order-service、matching-engine、market-data-service 四個服務？每個服務的邊界是怎麼定的？
3. 你當初從單體走向事件驅動微服務的真正瓶頸是什麼？是 CPU、DB lock、I/O、連線池，還是程式結構可維護性？
4. 如果今天流量只有現在的十分之一，你還會維持這個拆分嗎？哪幾個服務其實可以先合併？
5. 這套系統中，哪些地方追求強一致，哪些地方接受最終一致？你怎麼向產品或前端解釋這件事？
6. 你怎麼定義這個系統的核心 SLA 或 SLO？下單 API、撮合延遲、推播延遲、資料正確性各自的目標是多少？
7. 這套架構裡最脆弱的節點是哪一個？如果它掛掉，整個系統會怎麼退化？
8. 你現在的服務切分是依照團隊協作邊界、資源隔離，還是技術限制切出來的？如果團隊成長到 10 人，你會怎麼再拆？

## 3. 交易領域模型與核心流程

1. 你支援哪些訂單型別與狀態？各狀態之間允許哪些轉移，不允許哪些轉移？
2. 限價單與市價單的處理路徑有什麼本質差異？資金鎖定策略為什麼不同？
3. 你怎麼定義 maker、taker，以及成交價格應該採用哪一方價格？為什麼？
4. 部分成交時，訂單狀態、剩餘數量、資金解鎖、成交紀錄分別怎麼更新？
5. 撤單的正確語意是什麼？是「嘗試撤單」還是「保證撤單成功」？在撮合中的邊界時刻怎麼處理？
6. Self-trade prevention 的規則是什麼？如果同一使用者自己撞自己的單，系統應該怎麼做？
7. 你為什麼使用 UUID v7 當訂單 ID？如果改成 snowflake 或資料庫 sequence，優缺點是什麼？
8. 交易所最重要的 correctness invariant 是哪些？你認為這個專案目前已經能保證到什麼程度？

## 4. 下單、資金鎖定與一致性設計

1. 你把下單拆成 TX1 與 TX2 的原因是什麼？兩段交易各自保證什麼？
2. 為什麼一定要先鎖資金、再建單、再發事件？順序如果顛倒會出現什麼 bug？
3. 買單與賣單的鎖資金計算邏輯是什麼？市價買單的預扣款你怎麼估？
4. 如果 TX1 成功但後續 Kafka 發送失敗，系統怎麼保證訂單不會消失？
5. 如果撮合成功但 TX2 結算失敗，為什麼不直接做補償交易，而是依賴事件重試與冪等？
6. 退款邏輯最容易出錯的地方在哪裡？尤其是市價單未完全成交與 STP 觸發時怎麼算？
7. 你如何避免「成交了但訂單狀態還停在 NEW」這種金融級錯誤？
8. 如果同一個帳戶同時被多筆訂單更新，怎麼保證不會 double spend 或 lost update？
9. 你怎麼證明資產守恆？不是口頭描述，而是實際資料庫層面的驗證方式。

## 5. Kafka、事件驅動與 Outbox

1. 你整體事件流有哪些 topic？每個 topic 的責任邊界是什麼？
2. 為什麼下單與撤單要走同一個 topic，且 partition key 用 symbol？如果改用 user_id 會出什麼事？
3. 你選擇 at-least-once，而不是 exactly-once 的原因是什麼？這是成本考量、實作複雜度，還是可觀測性？
4. 你怎麼解釋 Transactional Outbox 的價值？它解決的是哪一類雙寫風險？
5. Outbox worker 為什麼用資料庫批次拉取加上 SKIP LOCKED？這個設計對多 worker 有什麼好處？
6. 如果 outbox 已寫入，但 worker 一直發不出去，系統會出現哪些指標異常？你怎麼告警？
7. 為什麼在沒有 outbox 的 fallback 模式下，發 Kafka 失敗不 rollback TX1？這個 trade-off 合理嗎？
8. matching-engine 產生 settlement event 時，為什麼採原地重試而不是直接 return error 讓 consumer 重跑？
9. 如果 consumer 在發布 settlement event 之後、commit offset 之前 crash，怎麼保證不會重複撮合？
10. 事件 schema 未來怎麼演進？如果你要替事件加欄位或改欄位型別，如何做到向後相容？
11. 未知 event type 為什麼選擇記 log 後 commit，而不是卡住等待人工處理？這個決策有什麼風險？
12. 你有沒有設計 DLQ？如果沒有，哪些錯誤你認為應該重試，哪些錯誤應該隔離？

## 6. 撮合引擎、順序性與高可用

1. 撮合引擎核心資料結構是什麼？你如何保證價格優先、時間優先？
2. 單一撮合操作的時間複雜度是多少？在最差情況下瓶頸會出現在哪裡？
3. 為什麼撮合引擎放在記憶體而不是直接在資料庫做撮合？
4. 冷啟動時為什麼要從資料庫恢復 active orders？為什麼市價單不能恢復進 order book？
5. 你現在的 restore 流程如何保證引擎狀態與資料庫狀態一致？如果恢復期間還有新訂單進來怎麼辦？
6. Leader election 為什麼選 PostgreSQL，而不是 etcd、Consul、ZooKeeper 或 Redis？
7. 你提到 fencing token 防腦裂，請你完整講一次 split-brain 情境下它是怎麼保護資料的。
8. 舊 leader 假設網路抖動後復活，它可能會做出哪些過期寫入？你怎麼在資料層擋掉？
9. 如果某個 symbol 交易量極高，全部壓在同一 partition 與同一引擎實例上，你的擴展策略是什麼？
10. 現在是 symbol 粒度序列化處理，如果未來有 1000 個 symbol，你的 engine manager、topic、partition 規劃會怎麼演進？

## 7. 資料庫、鎖與併發控制

1. 你的交易主要依賴資料庫的哪些保證？row-level lock、transaction isolation、unique constraint、foreign key，各自扮演什麼角色？
2. 你為什麼要把訂單 ID 先排序，再逐筆做 SELECT FOR UPDATE？這是在避免哪一類 deadlock？
3. 除了訂單鎖之外，帳戶餘額更新也可能互相競爭，你怎麼降低帳戶更新的死鎖機率？
4. 你如何定義「一次正確的結算交易」？哪些資料表必須在同一個 transaction 裡一起成功或一起失敗？
5. 哪些唯一鍵或索引是 correctness 關鍵，而不只是效能優化？
6. 你怎麼處理重複 settlement event？是靠 trade ID unique key、order 狀態檢查，還是其他機制？
7. order-service 啟動時自動跑 schema migration，這在單實例很方便，但多實例部署時會有什麼風險？
8. 如果 PostgreSQL 延遲升高，你預期最先爆的是哪一段流程？TX1、TX2、outbox worker，還是查詢 API？
9. 你做過哪些 audit SQL 來找 stuck locked funds、stuck orders、trade/order 對帳異常？各自代表什麼問題？

## 8. Redis、快取、限流與冪等

1. 你把 Redis 用在哪幾個地方？order book snapshot、rate limiting、idempotency，各自的容錯策略是什麼？
2. 市價單估算資金依賴 Redis 的 order book snapshot，如果 Redis 是冷的、舊的或 miss，你怎麼避免估錯？
3. matching-engine 啟動後先預熱 Redis snapshot 的設計，是在解哪個實際問題？
4. 你文件中提到 sliding window，但實作看起來更像 token bucket；如果面試官指出這件事，你會怎麼解釋？
5. Rate limiter 為什麼用 Lua script？如果不用 Lua，會有什麼 race condition？
6. Redis 掛掉時 gateway 退回記憶體版限流與冪等儲存，這在多實例環境有什麼風險？
7. Idempotency-Key 的生命週期應該多長？太短與太長各有什麼問題？
8. 如果同一個重試請求先打到 A gateway，再打到 B gateway，而當下 Redis 剛好不可用，你怎麼看這個風險？

## 9. API Gateway、WebSocket 與安全

1. Gateway 的主要價值是什麼？只是反向代理，還是安全邊界與流量整形層？
2. 哪些 API 被歸類為 public，哪些是 private？你分類的原則是什麼？
3. 你如何處理 JWT 驗證、使用者身分、以及 gateway 對下游服務的信任傳遞？
4. WebSocket 為什麼要獨立成 market-data-service，而不是留在 order-service 裡？
5. 你目前推的是 order book snapshot、trade、order update，為什麼選 snapshot 而不是 incremental diff？
6. 如果某個客戶端是 slow consumer，你的背壓策略是丟棄舊訊息、合流、限速，還是直接斷線？
7. 訂單更新如果是使用者私有事件，WebSocket 層如何做授權與資料隔離？如果現在還沒做完整，缺口在哪？
8. 客戶端斷線重連後，怎麼補資料？是靠重新拉 snapshot，還是服務端保留 replay 能力？

## 10. 可觀測性、SRE 與故障處理

1. 你為什麼用四大黃金信號來設計監控，而不是只看 CPU 與 memory？
2. 對這種交易系統來說，你認為最重要的三個告警是什麼？為什麼？
3. 你怎麼從 metrics 分辨「Kafka 壞了」、「DB 慢了」、「Redis 掛了」以及「WS 廣播塞住了」？
4. Outbox backlog、consumer lag、leader 狀態、WS dropped count 這些指標各自代表什麼風險？
5. 你現在的觀測缺口是什麼？例如 Go runtime、heap、goroutine、Kafka lag、DB exporter，有哪些還沒補齊？
6. 如果 P95 latency 持續上升，但 error rate 仍然很低，你會先懷疑哪幾層？
7. 你怎麼做跨服務問題定位？目前有 request ID、event correlation ID、distributed tracing 嗎？如果沒有，怎麼補？
8. 當你說「系統可以恢復」時，你的恢復定義是什麼？只要服務活著，還是要 correctness audit 也通過才算？

## 11. 測試策略、Correctness 與壓測證據

1. 你目前的測試分成 unit、integration、e2e、concurrency、race、k6 baseline，這幾層各自驗證什麼？
2. 撮合引擎最關鍵的測試案例是哪些？價格優先、時間優先、部分成交、連續成交、STP，你怎麼覆蓋？
3. 為什麼 race detector 對這個專案特別重要？它能抓到哪些你用一般單測抓不到的問題？
4. 你的壓測不是只看 TPS，還看 correctness audit。請你完整說明四個 audit SQL 各在驗證什麼。
5. 為什麼 baseline load test 跑過，不能直接宣稱系統已具備 production-grade correctness、resilience、soak coverage？
6. 本地測試與 ECS 測試各適合回答哪些問題？哪些結論你明確不接受只靠本地數據？
7. 如果 spike test 時大量 429 出現，但 5xx 很低，你會如何解讀這個結果？這算成功還是失敗？
8. 你怎麼找容量拐點？是看 P95 超過基線多少、error rate、consumer lag，還是 DB active connections？
9. 韌性測試如果要故障注入 Kafka、Redis、PostgreSQL，你預期各自應該出現什麼症狀，恢復後要驗哪些結果？
10. Soak test 最重要的不是瞬時值，而是趨勢。你會盯哪些慢性洩漏指標？如果現在缺 Go runtime metrics，你怎麼補救？

## 12. 部署、雲端與營運成熟度

1. 為什麼選 ECS、Terraform、ecspresso 這組合？如果改用 Kubernetes，你認為真正的收益是什麼？
2. 你現在既保留微服務，也保留 monolith 路徑，這是過渡期策略還是長期設計？風險是什麼？
3. Terraform plan 中哪些資源是這個系統能跑起來的關鍵基礎？VPC、RDS、Redis、Kafka、ALB、ECS 各自有什麼依賴關係？
4. 你如何做環境切分與配置管理？本地、test、staging、production 有哪些參數絕對不能混用？
5. 部署 matching-engine 新版本時，怎麼避免 leader 切換期間造成事件重複處理或長時間不可用？
6. 回滾策略是什麼？程式回滾、資料庫 schema 回滾、Kafka event schema 相容，這三件事要怎麼配合？
7. 目前健康檢查只有 health endpoint 夠不夠？readiness 與 liveness 應該如何區分？
8. 如果要做 secrets management，你會放在哪裡管理？環境變數、SSM、Secrets Manager，各有什麼考量？
9. 你怎麼估這套系統的基礎成本？哪個元件最有可能成為主要成本來源？
10. 如果今天要上 production，你認為還缺哪三件最不能省的營運能力？

## 13. 高壓追問與反思題

1. 你做這個專案時，哪一個技術決策最有把握，哪一個其實是當下先做出來、之後最想重構的？
2. 如果我要求你把這套系統提升到更接近 production-grade，你接下來 3 個月會優先做哪三件事？順序為什麼？
3. 哪些地方你敢說「已經驗證過」，哪些地方你只能誠實說「設計有了，但證據還不夠」？
4. 如果交易量成長 10 倍，你認為第一個瓶頸會在哪裡？資料庫、單一 symbol partition、WS fanout，還是別的？
5. 如果未來要支援多交易對、大量熱 symbol，你會怎麼重新設計 partition 與 matching topology？
6. 如果監管要求你做到完整審計、可追溯、不可抵賴，你現在的 event、trade、accounting 設計還缺什麼？
7. 你認為這個專案最容易被質疑「其實只是 demo，不是真正交易所後端」的點有哪些？你會怎麼回應？
8. 如果面試官說「你這套設計很複雜，對現在規模過度設計了」，你會怎麼辯護，又會在哪裡承認確實有成本？
9. 如果要讓新同事兩週內接手這個系統，你會怎麼安排 onboarding 路線？先看哪幾個流程、哪幾類測試、哪幾個 runbook？
10. 請你自己當 reviewer，說出這個專案目前最危險的 5 個技術風險，並給出每一個的緩解計畫。

## 14. 建議優先準備的題目

如果你要真的拿這份專案去面試，建議先把下面幾題練到可以穩定講 3 到 5 分鐘：

1. 服務切分原因與資料流設計
2. TX1 / TX2 與資金一致性
3. Transactional Outbox 與 Kafka 冪等
4. 撮合順序、價格優先、時間優先
5. Leader election 與 fencing token 防腦裂
6. Redis 快取冷啟動與市價單估算
7. Rate limiter 與 idempotency 的容錯邊界
8. Correctness audit SQL 與壓測證據
9. WebSocket slow consumer 與背壓策略
10. ECS / Terraform / ecspresso 的部署與回滾策略
