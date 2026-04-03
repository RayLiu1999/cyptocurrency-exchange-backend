package marketdata

import (
"context"

"github.com/RayLiu1999/exchange/internal/domain"
"github.com/RayLiu1999/exchange/internal/matching/engine"
)

// QueryService 處理市場行情查詢讀取
type QueryService interface {
	GetOrderBook(ctx context.Context, symbol string) (*engine.OrderBookSnapshot, error)
	GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*domain.KLine, error)
	GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*engine.Trade, error)
}

// TradeRepository 定義交易資料查詢介面
type TradeRepository interface {
	GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*domain.KLine, error)
	GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*engine.Trade, error)
}
