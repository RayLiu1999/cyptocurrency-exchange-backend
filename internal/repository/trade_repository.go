package repository

import (
	"context"

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

func (r *PostgresRepository) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*core.KLine, error) {
	executor := r.getExecutor(ctx)

	// 注意：這裡是一個簡化版的 K 線查詢，實際上應該要預先計算或使用 TimescaleDB
	// 這裡示範如何從 trades 原始表動態聚合。
	// $1: symbol, $2: interval (e.g., '1 minute'), $3: limit
	query := `
		SELECT 
			time_bucket($2, created_at) AS bucket,
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

	// 如果沒有安裝 TimescaleDB，可以用原生 SQL 模擬時鐘桶
	// 為了保證通用性，這裡假設有底層支援或改用原生時鐘聚合
	rows, err := executor.Query(ctx, query, symbol, interval, limit)
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
	return trades, nil
}

func (r *PostgresRepository) DeleteAllTrades(ctx context.Context) error {
	executor := r.getExecutor(ctx)
	_, err := executor.Exec(ctx, "DELETE FROM trades")
	return err
}
