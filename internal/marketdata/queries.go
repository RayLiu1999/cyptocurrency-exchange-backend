package marketdata

import (
	"context"
	"strings"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"go.uber.org/zap"
)

type queryServiceImpl struct {
	tradeRepo TradeRepository
	cacheRepo domain.CacheRepository
}

func NewQueryService(tradeRepo TradeRepository, cacheRepo domain.CacheRepository) QueryService {
	return &queryServiceImpl{
		tradeRepo: tradeRepo,
		cacheRepo: cacheRepo,
	}
}

// GetOrderBook 取得即時訂單簿
// [Redis 升級] 從 Redis 讀取。若無則退回給 error，因這裡已經是純微服務的查詢分離，
// orderbook 的來源只能是 matching-engine 的快取發布。
func (s *queryServiceImpl) GetOrderBook(ctx context.Context, symbol string) (*engine.OrderBookSnapshot, error) {
	if s.cacheRepo != nil {
		if snapshot, err := s.cacheRepo.GetOrderBookSnapshot(ctx, symbol); err == nil && snapshot != nil {
			logger.Log.Info("🚀 [Redis Cache] Hit", zap.String("symbol", symbol))
			return snapshot, nil
		}
		logger.Log.Info("💨 [Redis Cache] Miss", zap.String("symbol", symbol))
	}
	// 若 cache miss 或快取未啟用，回傳預設的空掛單簿，而不是回傳 Error 導致 500
	return &engine.OrderBookSnapshot{
		Symbol: symbol,
		Bids:   []engine.OrderBookLevel{},
		Asks:   []engine.OrderBookLevel{},
	}, nil
}

func (s *queryServiceImpl) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*domain.KLine, error) {
	symbol = strings.ToUpper(symbol)
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.tradeRepo.GetKLines(ctx, symbol, interval, limit)
}

func (s *queryServiceImpl) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*engine.Trade, error) {
	symbol = strings.ToUpper(symbol)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.tradeRepo.GetRecentTrades(ctx, symbol, limit)
}
