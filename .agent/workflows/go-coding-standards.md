---
description: Go 程式碼開發規範與範例
---

# Go 程式碼開發規範

## 檔案與 Package 規則

1. **每個 Go 檔案只引入必要的 package**，避免引入重複或未使用的 package
2. 使用 `goimports` 或 `gofmt` 自動整理 import

## 專案分層架構

```
internal/
├── core/           # Domain Layer (核心業務)
│   ├── domain.go   # 領域模型 (Order, Account, User)
│   ├── ports.go    # 介面定義 (Repository, Service)
│   └── service.go  # 業務邏輯實作
├── repository/     # Infrastructure Layer (資料存取)
│   └── postgres.go # PostgreSQL 實作
├── api/            # Presentation Layer (HTTP API)
│   └── handlers.go # Gin HTTP Handlers
└── infrastructure/ # 基礎設施 (Kafka, Redis)
```

## 註解規範

使用**繁體中文**撰寫註解：

```go
// PlaceOrder 處理用戶下單請求
// 會先檢查餘額是否足夠，然後鎖定資金並建立訂單
func (s *exchangeService) PlaceOrder(ctx context.Context, order *Order) error {
    // 1. 驗證訂單參數
    if err := validateOrder(order); err != nil {
        return fmt.Errorf("訂單驗證失敗: %w", err)
    }
    
    // 2. 鎖定用戶資金
    if err := s.accountRepo.LockFunds(ctx, order.UserID, amount); err != nil {
        return fmt.Errorf("資金鎖定失敗: %w", err)
    }
    
    // 3. 建立訂單
    return s.orderRepo.CreateOrder(ctx, order)
}
```

## 錯誤處理

使用 `fmt.Errorf` 包裝錯誤，提供上下文：

```go
if err != nil {
    return fmt.Errorf("查詢用戶 %d 失敗: %w", userID, err)
}
```

## 介面設計 (Ports)

在 `internal/core/ports.go` 定義介面：

```go
// OrderRepository 定義訂單資料存取介面
type OrderRepository interface {
    CreateOrder(ctx context.Context, order *Order) error
    GetOrderByID(ctx context.Context, id int64) (*Order, error)
    UpdateOrderStatus(ctx context.Context, id int64, status OrderStatus) error
}
```

## 測試規範

```go
func TestPlaceOrder_Success(t *testing.T) {
    // Arrange
    mockRepo := NewMockOrderRepository()
    service := NewExchangeService(mockRepo)
    
    // Act
    err := service.PlaceOrder(ctx, order)
    
    // Assert
    assert.NoError(t, err)
}
```

## 執行程式碼檢查

```bash
make lint   # 執行 golangci-lint
make fmt    # 格式化程式碼
make test   # 執行測試
```
