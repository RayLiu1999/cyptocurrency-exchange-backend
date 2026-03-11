# 交易流程解析 (1)：HTTP 請求與訂單建立

本系列文件將帶你走過一筆訂單從「使用者按下下單按鈕」到「最終結算並推播給前端」的完整生命週期。

## 1. 入口：HTTP Request (`api/order_handler.go`)
當使用者在前端點擊下單，第一站會抵達後端的 REST API Handler。

### 核心功能
負責接收 JSON 請求、基本參數驗證、從 JWT Token 中取得發起請求的 `UserID`，最後轉交給核心業務邏輯 (`ExchangeService`)。

```go
// CreateOrder 接收下單請求
func (h *OrderHandler) CreateOrder(c *gin.Context) {
    // 1. 綁定 JSON 到 Request DTO
    var req CreateOrderRequest
    if err := c.ShouldBindJSON(&req); err != nil { ... }

    // 2. 從 Context (Token) 中取得 UserID
    userID := c.MustGet("userID").(uuid.UUID)

    // 3. 建立內部領域模型 (Domain Model) Order
    order := &core.Order{
        UserID:   userID,
        Symbol:   req.Symbol,    // e.g., "BTC-USD"
        Side:     req.Side,      // BUY 或 SELL
        Type:     req.Type,      // LIMIT (限價) 或 MARKET (市價)
        Price:    req.Price,     
        Quantity: req.Quantity,
    }

    // 4. 呼叫核心業務邏輯
    err := h.exchangeService.PlaceOrder(c.Request.Context(), order)
    
    // 5. 回傳 HTTP 結果
    c.JSON(http.StatusCreated, gin.H{"message": "訂單已建立", "id": order.ID})
}
```

---

## 2. 核心業務邏輯起點：下單預檢與鎖倉 (`core/order_service.go`)
進入 `PlaceOrder` 後，我們首先要確保使用者有足夠的錢，並把這筆錢「凍結」起來，保證後續撮合時不會發生違約。

### 核心步驟與設計考量
*   **防止雙重花費 (Double Spending)**：在把訂單送進記憶體撮合引擎之前，**必須**先在資料庫層（DB Transaction）把資金鎖定。
*   **避免裂腦 (Split-Brain)**：如果反過來，先撮合再扣款，一旦資料庫掛掉，就會產生「市場顯示已成交，但使用者沒扣到錢」的嚴重 BUG。因此順序永遠是：`DB 鎖倉 -> 記憶體撮合 -> DB 結算`。

### 程式碼拆解 (PlaceOrder Part 1)

```go
func (s *ExchangeServiceImpl) PlaceOrder(ctx context.Context, order *Order) error {
    // 1. 基本數值校驗與正規化 (捨入到小數點後 8 位)
    order.Symbol = strings.ToUpper(order.Symbol)
    order.Price = order.Price.Round(8)
    order.Quantity = order.Quantity.Round(8)

    // 2. 計算需要鎖定的幣種與金額
    // - 限價買單: 鎖定 Quote 幣 (如 USD = Price * Quantity)
    // - 限價賣單: 鎖定 Base 幣 (如 BTC = Quantity)
    // - 市價買單: 需估算最大可能花費再加乘緩衝 (如 5%)
    currencyToLock, amountToLock, err := s.calculateLockAmount(order)

    // 3. 初始化訂單基本資訊 (使用 UUID v7 增進 B-Tree 寫入效能)
    order.ID, _ = uuid.NewV7()
    order.Status = StatusNew
    order.FilledQuantity = decimal.Zero
    // ...

    // 4. 第一個資料庫事務 (DB Transaction)：資金凍結與訂單落地
    err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
        // 扣除並凍結使用者可用餘額 (Available -> Locked)
        if err := s.accountRepo.LockFunds(ctx, order.UserID, currencyToLock, amountToLock); err != nil {
            return fmt.Errorf("餘額不足: %w", err)
        }
        // 將初始狀態 (StatusNew) 的訂單寫入資料庫
        if err := s.orderRepo.CreateOrder(ctx, order); err != nil {
            return fmt.Errorf("建立訂單失敗: %w", err)
        }
        return nil
    })
    
    // 如果這一步失敗 (例如餘額不足)，直接返回錯誤，訂單不會進入市場。
    if err != nil { return err }

    // 後續步驟：交由記憶體撮合引擎 (第二篇討論)
    // ...
}
```

### 總結
在這一階段，訂單已經成功在資料庫建立，且所需的交易保證金已經被凍結。此時這筆訂單才算有了進入「撮合引擎（市場）」的入場券。

👉 **下一篇**：[交易流程解析 (2)：在記憶體中閃電撮合 (Matching Engine)](02_matching_engine.md)
