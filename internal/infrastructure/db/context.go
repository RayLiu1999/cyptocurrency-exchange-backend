package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type txKeyType struct{}

var TxKey = txKeyType{}

// GetTx 從 context 中取得 pgx.Tx，如果沒有則回傳 nil
func GetTx(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(TxKey).(pgx.Tx)
	return tx
}
