# 微服務交易所系統流程圖解 (淺顯易懂版)

這份文件詳細描述了本系統在六種最常見情境下的運作流程。我們會把**訂單服務 (Order Service)**、**撮合引擎 (Matching Engine)** 與 **行情服務 (Market Data Service)** 這三個核心角色分開來看，讓你清楚知道每個動作「到底是由誰做、什麼時候做」，並搭配循序圖幫助理解。

---

## 1. 下限價單 (Place Limit Order)

**情境**：使用者掛了一張不會馬上全部成交的限價單，這筆單子會被收進系統的掛單簿等待其他人來吃單。

1. **鎖定資金 (TX1)**：訂單服務先在資料庫建立一筆 `Status=New` 的訂單，並把使用者的錢「鎖起來」避免超花。
2. **非同步通知**：把這張新訂單丟入 Kafka (`exchange.orders`)。
3. **記憶體排隊**：撮合引擎收到訂單後，放進自己記憶體裡的掛單簿。因為掛單簿變了，所以立刻發送出盤口更新事件 (`exchange.orderbook`)。
4. **推播給所有人**：行情服務收到更新，會去改寫 Redis 快照，再把最新的盤口畫面透過 WebSocket 廣播給所有開著網頁的人看。

```mermaid
sequenceDiagram
    autonumber
    actor User as 使用者 (前端)
    participant OS as 訂單服務 (OS)
    participant DB as 資料庫 (PostgreSQL)
    participant Kafka as 訊息隊列 (Kafka)
    participant ME as 撮合引擎 (ME)
    participant MD as 行情服務 (MD)
    participant Redis as 快取 (Redis)

    User->>OS: 發起下限價單請求 (API)
    OS->>DB: 建立訂單，並凍結相應資金 (TX1)
    OS->>Kafka: 丟入 exchange.orders 主題
    OS-->>User: 告訴使用者：訂單已送出，等待撮合
    Kafka->>ME: 撮合引擎消費新訂單
    ME->>ME: 沒人吃單，加入記憶體掛單簿 (Orderbook)
    ME->>Kafka: 掛單變了，發送盤口更新 exchange.orderbook
    Kafka->>MD: 行情服務收到更新事件
    MD->>Redis: 更新最新的盤口快照
    MD-->>User: 透過 WebSocket 推播最新盤口 (讓大家都看到)
```

---

## 2. 下市價單 (Place Market Order)

**情境**：使用者不看價格，就是要「現在立刻買到」或「現在立刻賣掉」。市價單不在掛單簿上排隊，沒買完的會直接被系統取消 (Cancel)。

1. **直接撞單**：引擎拿到市價單後，直接去撞記憶體裡的掛單簿，產生一筆或多筆「成交紀錄 (Trade)」。
2. **結算資金 (TX2)**：引擎打包這些成交紀錄發出結算事件 (`exchange.settlements`)。
3. **退還餘額**：訂單服務收到結算請求，把原本多凍結的錢退還、把剛買到的錢加給帳戶，並將訂單狀態改為 `Filled`。

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant OS as 訂單服務 (OS)
    participant DB
    participant Kafka
    participant ME as 撮合引擎 (ME)

    User->>OS: 發起市價單請求
    OS->>DB: 建立訂單，凍結預估最大資金 (TX1)
    OS->>Kafka: 丟入 exchange.orders 主題
    Kafka->>ME: 撮合引擎消費市價單
    ME->>ME: 立刻與目前的掛單簿對撞，產生「成交 (Trades)」
    ME->>Kafka: 發出盤口更新 exchange.orderbook
    ME->>Kafka: 發出結算請求 exchange.settlements (帶有剛剛的成交紀錄)
    Kafka->>OS: 訂單服務收到結算事件
    OS->>DB: 執行結算 (TX2)：扣款給賣家、給買家幣
    OS->>DB: 把市價單狀態改為 Filled (沒買足的轉 Canceled)
```

---

## 3. 取消訂單 (Cancel Order)

**情境**：使用者覺得等太久了，想把原本掛在簿子上的限價單抽走。

1. **請求撤單**：訂單服務發送「取消訂單請求」給引擎，但**這時候還沒改變資料庫的狀態**（因為引擎可能剛好把這單撮掉了，引擎說了算）。
2. **引擎拔單**：引擎在記憶體裡找到這單，把它抽掉，並發出 `OrderCanceledEvent` 給訂單服務。
3. **退還資金 (TX2)**：訂單服務收到引擎確認撤單的事件後，把這筆單剩下還沒成交部位的錢，解凍還給使用者，並在資料庫把狀態更新為 `Canceled`。

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant OS as 訂單服務 (OS)
    participant DB
    participant Kafka
    participant ME as 撮合引擎 (ME)

    User->>OS: 請求取消限價單
    OS->>DB: 檢查訂單到底是不是你的，還有沒有成交完
    OS->>Kafka: 丟入 exchange.orders 請求引擎撤單
    Kafka->>ME: 撮合引擎收到撤單請求
    ME->>ME: 從掛單簿裡把這張單子抽走
    ME->>Kafka: 掛單變了，發送盤口更新 exchange.orderbook
    ME->>Kafka: 通知訂單服務撤單成功 exchange.settlements (CanceledEvent)
    Kafka->>OS: 訂單服務收到確定撤單的事件
    OS->>DB: 執行結算 (TX2)：把掛單凍結的錢退還給使用者
    OS->>DB: 更新訂單為 Canceled
```

---

## 4. WebSocket 撈盤口與即時推播

**情境**：前端一打開網頁，畫面需要瞬間顯示當前盤口，之後還要跟著市場跳動。

1. **初始化 (Snapshot)**：一連上，行情服務會先去 Redis 拿完整的一包盤口快照給你。
2. **差異更新 (Delta)**：之後只要引擎有動作（有人下單、撤單），行情服務就會透過 WS 推播給你這一次「動了哪裡」。

```mermaid
sequenceDiagram
    autonumber
    actor Browser as 瀏覽器前台
    participant MD as 行情服務 (MD)
    participant Redis
    participant ME as 撮合引擎 (ME)
    participant Kafka

    Browser->>MD: 建立 WebSocket 連線 (我要看 BTC-USD)
    MD->>Redis: 去快取拿最新的完整盤口
    Redis-->>MD: 給你一份完整版
    MD-->>Browser: WS 送出完整版，讓畫面先出來
    
    loop 接著無止盡的推播
        ME->>ME: 有人交易產生了新盤口
        ME->>Kafka: 送出盤口變更 exchange.orderbook
        Kafka->>MD: 行情服務收到
        MD->>Redis: 複寫 Redis 快照 (確保下一個進首頁的人看到新的)
        MD-->>Browser: WS 送出盤口更新資料給前端跳動
    end
```

---

## 5. 系統重啟 (System Restart)

**情境**：工程師更新了撮合引擎，重啟服務。此時系統要如何找回重啟前大家掛的單子？

1. **Hydration (補水)**：引擎剛起來，還在盲目狀態，先去 PostgreSQL 問「目前狀態還是活躍的限價單有哪些？」。
2. **重建與打快照**：找回資料後，放進記憶體把盤口重疊起來，接著**主動打一份最新的盤口快照送到 Redis 裡**，並透過 Kafka 廣播給行情服務，避免外面看盤是空的。
3. **恢復接單**：從最新的 (`latest`) Kafka offset 開始聽新單子，繼續平常的工作。

```mermaid
sequenceDiagram
    autonumber
    participant ME as 撮合引擎 (ME)
    participant DB as 資料庫
    participant MD as 行情服務 (MD)
    participant Redis
    participant Kafka

    Note over ME: === 系統啟動 / 重啟 ===
    ME->>DB: 啟動第一步：給我所有 Status=New/Partial 的單
    DB-->>ME: 這裡有 3000 張沒撮完的單
    ME->>ME: 還原每一張單進記憶體，疊成最終盤口
    
    loop 每個交易對 (如 BTC-USD)
        ME->>Kafka: 主動廣播這疊好的盤口 exchange.orderbook
        ME->>Redis: 主動把這包快照塞進 Redis 永久保存
    end
    
    Kafka->>MD: 行情服務收到復原盤口，更新給 WebSocket 前端
    Note over ME: === 復原完成，開始接客 ===
    ME->>Kafka: 從 Latest 的點位開始聽 exchange.orders
```

---

## 6. 前端觸發模擬器 (Frontend Triggers Simulator)

**情境**：剛起專案沒人玩，利用模擬器腳本來自動「假扮幾百個玩家」亂標價格造市。

1. **啟動模擬**：透過 API 打給 Simulation Service。
2. **腳本迴圈**：模擬器拿著不同的 `User-ID` 與隨機數值產生各種造市單。
3. **化身玩家**：它其實就是一直在偷偷打訂單服務的 API，後面的動作就跟第一、二種情境完全一模一樣。

```mermaid
sequenceDiagram
    autonumber
    actor Admin as 開發者
    participant Sim as 模擬服務 (Simulation)
    participant OS as 訂單服務 (OS)
    participant ME as 撮合引擎 (ME)
    
    Admin->>Sim: API: POST /api/v1/simulation/start (開始造市)
    
    loop 每隔幾毫秒
        Sim->>Sim: 隨機決定：這次下市價買，還是限價賣？
        Sim->>OS: 就像真玩家一樣，打 API 給 Order Service
        Note right of OS: 進入標準的「扣資金、丟隊列」流程
        OS->>ME: 引擎收到，一陣亂撮產生流動性
    end
    
    Admin->>Sim: API: POST /api/v1/simulation/stop
    Sim->>Sim: 中止內部造市迴圈
```
