package core

import (
	"context"
	"fmt"
	"log"
)

// ClearSimulationData 清除交易資料
func (s *ExchangeServiceImpl) ClearSimulationData(ctx context.Context) error {
	return s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if err := s.tradeRepo.DeleteAllTrades(ctx); err != nil {
			return fmt.Errorf("清除成交資料失敗: %w", err)
		}
		if err := s.orderRepo.DeleteAllOrders(ctx); err != nil {
			return fmt.Errorf("清除訂單資料失敗: %w", err)
		}
		log.Println("✅ 已清除交易資料")
		return nil
	})
}
