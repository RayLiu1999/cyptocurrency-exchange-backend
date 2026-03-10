package repository

import (
	"context"
	"time"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/core/matching"
)

// --- TradeRepository Implementation ---

func (r *PostgresRepository) CreateTrade(ctx context.Context, trade *matching.Trade) error {
	executor := r.getExecutor(ctx)
	query := `
		INSERT INTO trades (id, symbol, maker_order_id, taker_order_id, price, quantity, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := executor.Exec(ctx, query,
		trade.ID, trade.Symbol, trade.MakerOrderID, trade.TakerOrderID,
		trade.Price, trade.Quantity, trade.CreatedAt)
	return err
}

func parseIntervalMs(interval string) int64 {
	switch interval {
	case "1m":
		return 60 * 1000
	case "3m":
		return 3 * 60 * 1000
	case "5m":
		return 5 * 60 * 1000
	case "15m":
		return 15 * 60 * 1000
	case "30m":
		return 30 * 60 * 1000
	case "1h":
		return 60 * 60 * 1000
	case "2h":
		return 2 * 60 * 60 * 1000
	case "4h":
		return 4 * 60 * 60 * 1000
	case "6h":
		return 6 * 60 * 60 * 1000
	case "12h":
		return 12 * 60 * 60 * 1000
	case "1d":
		return 24 * 60 * 60 * 1000
	case "1w":
		return 7 * 24 * 60 * 60 * 1000
	default:
		return 60 * 1000 // default 1m
	}
}

func (r *PostgresRepository) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*core.KLine, error) {
	executor := r.getExecutor(ctx)

	intervalMs := parseIntervalMs(interval)

	// 不依賴 TimescaleDB，純粹利用 BIGINT 整數除法來分桶
	// $1: symbol, $2: intervalMs, $3: limit
	query := `
		SELECT 
			(created_at / $2::bigint) * $2::bigint AS bucket,
			(array_agg(price ORDER BY created_at ASC))[1] AS open,
			MAX(price) AS high,
			MIN(price) AS low,
			(array_agg(price ORDER BY created_at DESC))[1] AS close,
			SUM(quantity) AS volume
		FROM trades
		WHERE symbol = $1
		GROUP BY bucket
		ORDER BY bucket DESC
		LIMIT $3`

	rows, err := executor.Query(ctx, query, symbol, intervalMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var klines []*core.KLine
	for rows.Next() {
		var k core.KLine
		err := rows.Scan(&k.Timestamp, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume)
		if err != nil {
			return nil, err
		}
		klines = append(klines, &k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return klines, nil
}

func (r *PostgresRepository) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error) {
	executor := r.getExecutor(ctx)
	query := `
		SELECT id, symbol, maker_order_id, taker_order_id, price, quantity, created_at
		FROM trades
		WHERE symbol = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := executor.Query(ctx, query, symbol, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []*matching.Trade
	for rows.Next() {
		var t matching.Trade
		err := rows.Scan(&t.ID, &t.Symbol, &t.MakerOrderID, &t.TakerOrderID, &t.Price, &t.Quantity, &t.CreatedAt)
		if err != nil {
			return nil, err
		}
		trades = append(trades, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return trades, nil
}

func (r *PostgresRepository) DeleteAllTrades(ctx context.Context) error {
	executor := r.getExecutor(ctx)
	_, err := executor.Exec(ctx, "DELETE FROM trades")
	return err
}

// nowMilli 回傳目前的 Unix 毫秒，供 repository 層使用
func nowMilli() int64 {
	return time.Now().UnixMilli()
}
