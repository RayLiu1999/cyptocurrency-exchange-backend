package repository

import (
	"context"

"github.com/RayLiu1999/exchange/internal/order"
"github.com/RayLiu1999/exchange/internal/marketdata"

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
var _ order.OrderRepository = (*PostgresRepository)(nil)
var _ order.AccountRepository = (*PostgresRepository)(nil)
var _ order.UserRepository = (*PostgresRepository)(nil)
var _ order.TradeRepository = (*PostgresRepository)(nil)
var _ marketdata.TradeRepository = (*PostgresRepository)(nil)
var _ order.DBTransaction = (*PostgresRepository)(nil)

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

// ValidateFencingTokenTx 在當前的資料庫事務（TX）內部，
// 原子性地驗證傳入的 fencing token 是否代表目前合法的 Leader。
//
// 這是整個防腦裂機制的「最後一道鐵閘」。
// 它必須在 ExecTx 的閉包 Context 中被呼叫，
// 這樣這個 SELECT 就會和後續的帳戶餘額 UPDATE 在「同一個 DB Transaction」裡執行。
//
// 為什麼加 FOR SHARE？
//
//	一般的 SELECT 只是讀資料，讀完了這一行就「沒人管」。
//	FOR SHARE（共享鎖）會對 partition_leader_locks 這一列加鎖，
//	宣告：「這個結算事務跑完之前，不允許任何其他事務對這一列做 UPDATE / DELETE。」
//	也就是說，即使結算中途 GC 發呆了 5 秒，正在搶奪 Leader 位置的新機器想要
//	執行 UPSERT partition_leader_locks 也會被資料庫卡住，必須等到本筆結算 Commit 後才能繼續。
//	這消除了「驗證通過 → GC 發呆 → 殭屍 Leader 改換 Token → 繼續扣款」的 Race Condition 窗口。
func (r *PostgresRepository) ValidateFencingTokenTx(ctx context.Context, partition string, token int64) (bool, error) {
	// token <= 0 代表訊息來自舊版（未整合 FencingToken 的版本），允許通過以維持向後相容
	if token <= 0 {
		return true, nil
	}

	var currentToken int64
	// getExecutor(ctx) 是關鍵：當呼叫者在 ExecTx 的閉包內時，
	// ctx 中已注入了 pgx.Tx，getExecutor 會自動取出並使用同一個 Tx。
	// 這確保 SELECT FOR SHARE 和後續所有 UPDATE 都在同一個 Transaction 中原子執行。
	err := r.getExecutor(ctx).QueryRow(ctx, `
		SELECT fencing_token
		FROM partition_leader_locks
		WHERE partition = $1
		FOR SHARE`,
		partition,
	).Scan(&currentToken)

	if err != nil {
		// pgx.ErrNoRows = 資料庫中目前沒有任何 Leader 持有鎖
		// → 允許通過：代表系統可能剛啟動，後續的冪等性保護會兜底
		return true, nil
	}

	// token < currentToken：訊息攜帶的是舊一代 Leader 核發的令牌
	// → 這是殭屍訊息，必須拒絕，回傳 false 告知外層丟棄
	if token < currentToken {
		return false, nil
	}
	return true, nil
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
