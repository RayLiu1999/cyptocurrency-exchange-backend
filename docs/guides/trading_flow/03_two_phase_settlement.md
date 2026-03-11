# 交易流程解析 (3)：兩階段結算與狀態更新 (Two-Phase Settlement)

在上一回，撮合引擎（記憶體）產生了一堆 `Trade` (成交記錄)。現在我們回到 `PlaceOrder`，進入**第二個資料庫事務 (DB Transaction)**。

此階段的任務是：把剛剛在記憶體裡發生的事，通通「白紙黑字」寫進 PostgreSQL 資料庫。我們在這裡實施了特殊的「兩階段鎖定與結算」技巧來防止死鎖。

## 1. Phase 1: 資源標準化排序與獲取 Order 鎖
當一個 Taker 吃掉好多個 Maker 時，如果直接依序對 DB 的 Order Row 上鎖，遇到多個 Taker 交叉掃單就會死鎖 (`40P01 Deadlock`)。
解法是：**把要上鎖的 Order ID 全部收集起來，按字典排序（字母順序）後，再依序上鎖。**

```go
// Phase 1: 資源標準化排序與獲取 Order 鎖
// 1. 收集被掃到的所有的 Maker Order IDs (利用 Map 順便去重)
makerOrderIDsMap := make(map[uuid.UUID]bool)
for _, trade := range trades {
    makerOrderIDsMap[trade.MakerOrderID] = true
}

// 2. 轉為 Slice 並執行 UUID 的字串字典序排序
sort.Slice(makerOrderIDs, func(i, j int) bool {
    return makerOrderIDs[i].String() < makerOrderIDs[j].String()
})

// 3. 按照絕對的安全順序，依序呼叫 `FOR UPDATE` 行級悲觀鎖，保證絕不死鎖
lockedMakerOrders := make(map[uuid.UUID]*Order)
for _, id := range makerOrderIDs {
    makerOrder, _ := s.orderRepo.GetOrderForUpdate(ctx, id)
    lockedMakerOrders[id] = makerOrder
}
```

---

## 2. Phase 2: 狀態計算與資金聚合 (聚合結算器)
拿到了所有 Maker 的鎖之後，我們開始進行運算。此時**依然不碰資料庫**，只在本地變數裡計算資金與狀態的變化。

```go
// CalculateTradeSettlement (純計算函式，無視副作用)
func (s *ExchangeServiceImpl) CalculateTradeSettlement(trade *Trade, taker, maker *Order) []AccountUpdate {
    // 買賣雙方身分確認 ...
    // ...
    // ... 
    
    // 將一筆成交 (Trade) 拆解成 4 條「資金變動 (AccountUpdate)」
    // 買方花美金 (Amount: 負數, Unlock: 原本凍結的), 獲得比特幣 (Amount: 正數, Unlock: 0)
    // 賣方花比特幣 (Amount: 負數, Unlock: 扣掉比特幣), 獲得美金 (Amount: 正數, Unlock: 0)
    return []AccountUpdate{
        {UserID: buyer.UserID, Currency: quote, Amount: tradeValue.Neg(), Unlock: buyerUnlockAmount},
        {UserID: buyer.UserID, Currency: base, Amount: tradeQty, Unlock: decimal.Zero},
        {UserID: seller.UserID, Currency: base, Amount: tradeQty.Neg(), Unlock: tradeQty},
        {UserID: seller.UserID, Currency: quote, Amount: tradeValue, Unlock: decimal.Zero},
    }
}
```

在 `PlaceOrder` 裡，我們會將所有的 Maker 和 Taker 的結算紀錄塞進一個大陣列 `allAccountUpdates` 裡，最後連同「市價單未成交的保證金退款」一併交給 `AggregateAndSortAccountUpdates`：

```go
// 把同一位使用者的同一個幣種 (如 UserA 的 USD) 的多筆增減，合併為一筆
// 然後依照 UserID + Currency 做第二次資源排序（這同樣是為了解決死鎖問題）
aggregatedUpdates := AggregateAndSortAccountUpdates(allAccountUpdates)
```

---

## 3. Phase 3: 資料庫統一寫入 (Commit)
經過 Phase 1 拿了鎖，Phase 2 搞定了安全順序列表。最後一步，只需一股腦寫入！

```go
// 1. 寫入 Maker 新狀態 (Filled 量)
for _, id := range makerOrderIDs {
    makerOrder := lockedMakerOrders[id]
    s.orderRepo.UpdateOrder(ctx, makerOrder)
    s.tradeListener.OnOrderUpdate(makerOrder) // 觸發 Event
}

// 2. 寫入 Trade (成交歷史表)
for _, trade := range tradesToSave {
    s.tradeRepo.CreateTrade(ctx, trade)
    s.tradeListener.OnTrade(trade) // 觸發 Event
}

// 3. 寫入 Accounts (剛剛算好的聚合結算資金)
for _, up := range aggregatedUpdates {
    if up.Unlock > 0 { s.accountRepo.UnlockFunds(ctx, ...) }
    if up.Amount != 0 { s.accountRepo.UpdateBalance(ctx, ...) }
}

// 4. 寫入 Taker 新狀態 (這是他的整個大 DB 交易 Transaction Commit)
order.UpdatedAt = time.Now().UnixMilli()
s.orderRepo.UpdateOrder(ctx, order)
```

### 總結
一旦這裡的 `txManager.ExecTx` 返回成功，整筆訂單的錢就真實入帳了！這就是一個高性能撮合引擎兼顧並發速度與 ACID 一致性的真實樣貌。

但你可能會問：我們在上面呼叫的 `OnOrderUpdate` 和 `OnTrade` 是去哪了？ 這就得牽涉到我們的主動式即時通訊 (WebSocket) 系統了！

👉 **下一篇**：[交易流程解析 (4)：WebSocket 即時推播機制](04_websocket_broadcast.md)
