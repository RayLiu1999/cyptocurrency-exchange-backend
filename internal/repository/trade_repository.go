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

func (r *PostgresRepository) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*core.KLine, error) {
	executor := r.getExecutor(ctx)

	// 使用 time_bucket 函式聚合 K 線（需要 TimescaleDB 擴充）
	// created_at 為 BIGINT (UnixMilli)，需先轉換為 timestamp 再做時間桶聚合
	// $1: symbol, $2: interval (e.g., '1 minute'), $3: limit
	query := `
		SELECT 
			EXTRACT(EPOCH FROM time_bucket($2, to_timestamp(created_at / 1000.0))) * 1000 AS bucket,
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
