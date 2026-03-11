package core

import (
	"context"
	"strings"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"go.uber.org/zap"
)

// GetOrderBook 取得即時訂單簿
// [Redis 升級] 優先從 Redis 讀取，若無則 fallback 至 Memory Engine 並非同步回填快取
func (s *ExchangeServiceImpl) GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error) {
	// 1. 嘗試從快取讀取
	if s.cacheRepo != nil {
		if snapshot, err := s.cacheRepo.GetOrderBookSnapshot(ctx, symbol); err == nil && snapshot != nil {
			logger.Log.Info("🚀 [Redis Cache] Hit", zap.String("symbol", symbol))
			return snapshot, nil
		}
		logger.Log.Info("💨 [Redis Cache] Miss", zap.String("symbol", symbol))
	}

	// 2. Cache Miss: 從 Engine 讀取
	engine := s.engineManager.GetEngine(symbol)
	if engine == nil {
		return matching.NewOrderBookSnapshot(symbol), nil
	}
	snapshot := engine.GetOrderBookSnapshot(20)

	// 3. 非同步回填快取 (Write-Aside)
	if s.cacheRepo != nil {
		go func() {
			s.cacheRepo.SetOrderBookSnapshot(context.Background(), snapshot)
		}()
	}

	return snapshot, nil
}

func (s *ExchangeServiceImpl) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*KLine, error) {
	symbol = strings.ToUpper(symbol)
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.tradeRepo.GetKLines(ctx, symbol, interval, limit)
}

func (s *ExchangeServiceImpl) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error) {
	symbol = strings.ToUpper(symbol)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.tradeRepo.GetRecentTrades(ctx, symbol, limit)
}
