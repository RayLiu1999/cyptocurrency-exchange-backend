# 交易流程解析 (4)：WebSocket 即時推播機制

當結算完成後，前端如何立刻知道「我的訂單成交了」或是「掛單簿深度改變了」？這就是 WebSocket 發揮威力的地方。

## 1. 觸發事件 (`core/order_service.go`)
在上一篇的 `PlaceOrder` 結尾，我們看到了幾個神祕的呼叫：
```go
if s.tradeListener != nil {
    s.tradeListener.OnOrderUpdate(order)
    
    // 掛單簿已變更（新增/成交），推播最新深度
    snapshot := engine.GetOrderBookSnapshot(20)
    s.tradeListener.OnOrderBookUpdate(snapshot)
}
```
這裡的 `tradeListener` 是一個介面 (Interface)，而在系統啟動時，我們把 `api.WebSocketHandler` 注入了進去。這意味著核心業務邏輯完全不知道 WebSocket 的存在，它只是大喊：「嘿！我這筆訂單狀態改變了！」，剩下的交給實作者處理。

---

## 2. 轉譯為 JSON 訊息 (`api/websocket_handler.go`)
`WebSocketHandler` 收到事件後，會將 Go 結構體轉換成前端看得懂的 JSON 格式。

```go
// 推播掛單簿深度快照
func (h *WebSocketHandler) OnOrderBookUpdate(snapshot *matching.OrderBookSnapshot) {
    msg := map[string]any{
        "type": "depth_snapshot",
        "data": snapshot,
    }

    jsonMsg, _ := json.Marshal(msg)
    h.Broadcast(jsonMsg)
}

// 推播訂單狀態更新
func (h *WebSocketHandler) OnOrderUpdate(order *core.Order) {
    msg := map[string]any{
        "type": "order_update",
        "data": map[string]any{
            "id":              order.ID,
            "user_id":         order.UserID,
            // ... 省略其他欄位
            "status":          core.StatusToString(order.Status),
        },
    }

    jsonMsg, _ := json.Marshal(msg)
    h.Broadcast(jsonMsg)  // 發送廣播
}
```

---

## 3. 非阻塞廣播與 CSP 模型 (`api/websocket_handler.go`)
如果你有幾萬個使用者連線，直接用一個 `for` 迴圈去對每一個網路連線發送 `WriteMessage`，只要其中一個人網路卡住，整個撮合引擎甚至伺服器就會跟著卡死。

我們使用 Go 的 **CSP (Communicating Sequential Processes)** 模型與 Channel 來解決這個問題：

### 非阻塞的 Broadcast (核心不被卡死)
```go
func (h *WebSocketHandler) Broadcast(message []byte) {
    select {
    // 試著把訊息塞進 broadcast 信道
    case h.broadcast <- message: 
    default:
        // 如果 channel (緩衝區 256) 全滿了，直接丟棄！
        // 確保 OnOrderUpdate (在 PlaceOrder 裡) 永遠不會阻塞
        log.Println("WS Broadcast: channel full, dropping message")
    }
}
```

### 廣播分發中心 (Run)
這背後有一個永不停止的 Goroutine 負責把訊息分發給每一個客戶端 (Client)：
```go
func (h *WebSocketHandler) Run() {
    for {
        select {
        case message := <-h.broadcast:
            // 當有廣播訊息進來，逐一發給連線的使用者
            for client := range h.clients {
                select {
                case client.send <- message: 
                    // 成功把訊息放進該客戶的發送信道
                default:
                    // 🚨 如果這個客戶端自己專屬的發送信道滿了 (表示他網路極慢)
                    // 為了不拖累其他人，直接把他斷線踢掉！
                    close(client.send)
                    delete(h.clients, client)
                }
            }
        // ...
        }
    }
}
```

### 最終的網路 I/O (Write Pump)
每一個連線進來的使用者，都有一個專屬的 `writePump` Goroutine，只負責把信道理的資料「真的寫過網路卡」送到瀏覽器前端。
```go
func (c *Client) writePump() {
    for {
        select {
        case message, ok := <-c.send:
            // ...
            // 執行真正的網路寫入 (Blocking IO，但只會卡住這個專屬的 Goroutine)
            c.conn.WriteMessage(websocket.TextMessage, message)
        }
    }
}
```

### 總結
前端的 `useWebSocket.js` 收到這個 `depth_snapshot` 或是 `order_update` JSON 後，就會觸發 React 狀態更新，並將最新的數字呈現在畫面上！

----
🎉 **恭喜！** 你已經完整走過了一筆交易從下單、鎖倉、記憶體撮合、兩階段安全解算、一直到最後的 WebSocket 即時推播的深水區。這是現代高性能加密貨幣交易所的最核心架構！
