# 交易流程解析 (2)：在記憶體中閃電撮合 (Matching Engine)

在前一篇我們提到，Taker 訂單（發起方）在資料庫中成功凍結保證金並建立訂單後，就會被送到位於 **記憶體 (In-Memory)** 的核心撮合引擎進行配對。

## 1. 轉換格式與進入引擎 (`core/order_service.go`)
因為撮合是一個純數學運算，為求極致效能，我們會將原本攜帶許多 DB 狀態的 `core.Order` 轉換為輕量化的 `matching.Order`。

```go
// 轉換為精簡的撮合專用 Order 結構
matchOrder := s.convertToMatchingOrder(order)

// 根據交易對 (Symbol) 獲取對應的獨立撮合引擎
engine := s.engineManager.GetEngine(order.Symbol)

// ⚡ 開始閃電撮合，回傳一組 Trades（成交紀錄）
trades := engine.Process(matchOrder)
```

**為何要在記憶體中處理？**
傳統依賴資料庫 `UPDATE` 的撮合引擎效能極低（每秒頂多幾百筆，遇到高頻交易容易死鎖）。而把訂單簿 (Order Book) 丟進 Go 的變數（記憶體陣列/指標）裡做純邏輯運算，每秒可以執行百萬筆撮合。

---

## 2. 獲取引擎鎖防干擾 (`core/matching/engine.go`)
進入引擎後，第一件事就是「上鎖」。每個交易對都有自己獨立的 Mutex (互斥鎖)。

```go
func (e *Engine) Process(order *Order) []*Trade {
    // 🔒 上鎖：避免多個 Taker 同時吃掉同一個 Maker
    e.mu.Lock()
    defer e.mu.Unlock()

    var trades []*Trade
    // 判斷是買單還是賣單，進入不同的撮合邏輯
    if order.Side == SideBuy {
        trades = e.matchBuyOrder(order)
    } else {
        trades = e.matchSellOrder(order)
    }

    // 撮合結束後，如果訂單還沒成交完 (且不是市價單)，就掛在簿子上成為 Maker
    if order.Quantity.IsPositive() && order.Type != TypeMarket {
        e.orderBook.AddOrder(order)
    }

    return trades
}
```

---

## 3. 撮合邏輯與產生 Trade (`core/matching/engine.go`)
以「買單 (BuyOrder)」為例，我們會去掃描掛單簿上的 `Ask` (賣單列表)，找出最便宜的來成交。

```go
func (e *Engine) matchBuyOrder(buyOrder *Order) []*Trade {
    var trades []*Trade

    for {
        // 1. 取得紅字「賣方」最便宜的那一檔 (Best Ask)
        bestAsk := e.orderBook.BestAsk()
        
        // 沒賣單了 -> 無法撮合，直接退出迴圈
        if bestAsk == nil { break } 

        // 2. Wash Trade 預防機制
        // 避免自己買自己的掛單，通常這叫做「洗單」，交易所不允許
        if buyOrder.UserID == bestAsk.UserID { break }

        // 3. 價格檢查
        // 如果我是限價買單，且我的出價 < 最便宜的賣價，那當然不成交
        if buyOrder.Type != TypeLimit && buyOrder.Price.LessThan(bestAsk.Price) { break }

        // 4. 計算這一次能成交多少數量？ 取雙方的「最小值」
        matchQty := buyOrder.Quantity
        if bestAsk.Quantity.LessThan(matchQty) {
            matchQty = bestAsk.Quantity
        }

        // 5. 💳 產生一筆 成交紀錄 (Trade)
        trade := &Trade{
            ID:           uuid.NewV7(), // 使用 v7 增加後續落表效能
            Symbol:       e.orderBook.Symbol(),
            MakerOrderID: bestAsk.ID,
            TakerOrderID: buyOrder.ID,
            Price:        bestAsk.Price, // 💡 撮合規則：成交價一律以「掛在上面的人」為主
            Quantity:     matchQty,
        }
        trades = append(trades, trade)

        // 6. 雙方訂單的殘留數量減少
        buyOrder.Quantity = buyOrder.Quantity.Sub(matchQty)
        bestAsk.Quantity = bestAsk.Quantity.Sub(matchQty)

        // 7. 如果掛單 (Maker) 被完全吃光，就把他從訂單簿剔除！
        if bestAsk.Quantity.IsZero() {
            e.orderBook.RemoveBestAsk()
        }

        // 若我也買足了，退出迴圈
        if buyOrder.Quantity.IsZero() { break }
    }
    
    return trades // 把這些成交紀錄回傳到外頭
}
```

### 總結
Taker 訂單穿越記憶體訂單簿，像掃雷一樣把所有價格符合的 Maker 訂單一一吃掉，並留下一串 `Trade` 物件返回給呼叫端。至此，撮合完成，但**這些都還只是在 RAM 的數字變動**，一旦伺服器中途停電就會「假成交」。所以接下來，我們必須帶著這些 `trades`，前往資料庫進行「實質扣款與結算」。

👉 **下一篇**：[交易流程解析 (3)：兩階段鎖定與資金結算 (Two-Phase Settlement)](03_two_phase_settlement.md)
