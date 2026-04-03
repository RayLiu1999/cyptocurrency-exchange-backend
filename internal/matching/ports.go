package matching

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/domain"
)

// OrderRepository 定義撮合引擎啟動時需要的讀取訂單的介面
type OrderRepository interface {
	GetActiveOrders(ctx context.Context) ([]*domain.Order, error)
}
