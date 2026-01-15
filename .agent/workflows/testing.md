---
description: 測試執行與覆蓋率報告
---

# 測試執行流程

// turbo-all

## 執行所有測試

```bash
make test
```

## 執行特定套件測試

```bash
go test -v ./internal/core/...
go test -v ./internal/repository/...
```

## 產生覆蓋率報告

```bash
make test-coverage
```

報告會產生在：
- `coverage.txt` - 原始覆蓋率資料
- `coverage.html` - HTML 格式報告（可在瀏覽器開啟）

## 執行單一測試

```bash
go test -v -run TestPlaceOrder ./internal/core/
```

## 整合測試

使用 Testcontainers 啟動真實的 PostgreSQL：

```bash
go test -v -tags=integration ./internal/repository/...
```

## 測試覆蓋率目標

| 層級 | 目標覆蓋率 |
|-----|----------|
| `internal/core/` | ≥ 80% |
| `internal/repository/` | ≥ 70% |
| `internal/api/` | ≥ 60% |

## 測試範本

```go
func TestPlaceOrder_InsufficientFunds(t *testing.T) {
    // Arrange
    mockRepo := NewMockAccountRepository()
    mockRepo.On("GetBalance", mock.Anything, userID).Return(decimal.Zero, nil)
    
    service := NewExchangeService(mockRepo, orderRepo)
    
    // Act
    err := service.PlaceOrder(ctx, order)
    
    // Assert
    assert.ErrorContains(t, err, "餘額不足")
}
```
