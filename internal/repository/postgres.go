package repository

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Ensure PostgresRepository implements the interfaces
var _ core.OrderRepository = (*PostgresRepository)(nil)
var _ core.AccountRepository = (*PostgresRepository)(nil)
var _ core.UserRepository = (*PostgresRepository)(nil)
var _ core.TradeRepository = (*PostgresRepository)(nil)
var _ core.DBTransaction = (*PostgresRepository)(nil)

type txKeyType struct{}

var txKey = txKeyType{}

// ExecTx 執行交易
func (r *PostgresRepository) ExecTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// 將 tx 注入 context
		txCtx := context.WithValue(ctx, txKey, tx)
		return fn(txCtx)
	})
}

// DBExecutor 定義共用的 SQL 執行介面 (pgx.Conn 和 pgx.Tx 都實現了這個)
type DBExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// getExecutor 從 context 獲取 tx，如果沒有則返回 db pool
func (r *PostgresRepository) getExecutor(ctx context.Context) DBExecutor {
	if tx, ok := ctx.Value(txKey).(pgx.Tx); ok {
		return tx
	}
	return r.db
}

// Pool 回傳底層連線池，供整合測試建立清理用 SQL 使用
func (r *PostgresRepository) Pool() *pgxpool.Pool {
	return r.db
}
