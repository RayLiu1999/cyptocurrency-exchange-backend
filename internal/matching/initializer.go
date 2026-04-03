package matching

import (
	"context"
	"fmt"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"go.uber.org/zap"
)

// RestoreEngineSnapshot 從資料庫載入活動訂單至記憶體撮合引擎。
// 此方法專供 matching-engine 服務在冷啟動時呼叫。
func RestoreEngineSnapshot(ctx context.Context, orderRepo OrderRepository, engineManager *engine.EngineManager) error {
	orders, err := orderRepo.GetActiveOrders(ctx)
	if err != nil {
		return fmt.Errorf("載入活動訂單失敗: %w", err)
	}

	for _, order := range orders {
		// 防呆機制：市價單絕對不能進撮合引擎的掛單簿
		if order.Type != domain.TypeLimit {
			logger.Warn("警告：嘗試恢復非限價單進入掛單簿，已忽略", zap.String("OrderID", order.ID.String()))
			continue
		}

		eng := engineManager.GetEngine(order.Symbol)
		if eng != nil {
			var matchSide engine.OrderSide
			if order.Side == domain.SideBuy {
				matchSide = engine.SideBuy
			} else {
				matchSide = engine.SideSell
			}

			matchingOrder := &engine.Order{
				ID:       order.ID,
				UserID:   order.UserID,
				Side:     matchSide,
				Type:     engine.TypeLimit,
				Price:    order.Price,
				Quantity: order.Quantity.Sub(order.FilledQuantity),
			}
			eng.RestoreOrder(matchingOrder)
		}
	}
	logger.Info(fmt.Sprintf("✅ 成功從資料庫恢復 %d 筆活動訂單至撮合引擎", len(orders)))
	return nil
}
