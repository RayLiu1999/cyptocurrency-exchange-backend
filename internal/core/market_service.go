package core

import (
	"context"
	"strings"

	"github.com/RayLiu1999/exchange/internal/core/matching"
)

// GetOrderBook 取得即時訂單簿
func (s *ExchangeServiceImpl) GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error) {
	engine := s.engineManager.GetEngine(symbol)
	if engine == nil {
		return matching.NewOrderBookSnapshot(symbol), nil
	}
	return engine.GetOrderBookSnapshot(20), nil
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
