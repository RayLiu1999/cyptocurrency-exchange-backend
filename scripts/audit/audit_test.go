package audit_test

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDataConsistency_PostLoadTest 是用來確保系統在經歷極端高併發 (例如 K6 壓測) 後，
// 核心業務數據 (訂單、撮合成交、資產餘額) 並未產生不一致或毀損。
// 任何一個測試失敗都代表存在嚴重的系統漏洞或 Race Condition。
func TestDataConsistency_PostLoadTest(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable"
	}

	db, err := sql.Open("pgx", dbURL)
	require.NoError(t, err, "Failed to connect to database")
	defer db.Close()

	require.NoError(t, db.Ping(), "Database is not reachable")

	// 1. Stuck Locked Funds
	// 資金被鎖定但沒有活躍訂單
	t.Run("No Stuck Locked Funds", func(t *testing.T) {
		query := `
			SELECT count(*)
			FROM accounts a
			WHERE a.locked > 0
			AND NOT EXISTS (
				SELECT 1 FROM orders o
				WHERE o.user_id = a.user_id 
				AND o.status IN (1, 2) -- 1: NEW, 2: PARTIALLY_FILLED
			)
		`
		var count int
		err := db.QueryRow(query).Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 0, count, "Found %d accounts with stuck locked funds. This means funds were locked for an order, but the order was cancelled/filled/deleted without unlocking the funds.", count)
	})

	// 2. Stuck Orders
	// 訂單長期卡在 NEW 或 PARTIALLY_FILLED 狀態
	t.Run("No Stuck Orders", func(t *testing.T) {
		// 設定寬限期，過於陳舊的未處理訂單視為異常。這裡設為超過 5 分鐘。
		timeoutThreshold := time.Now().Add(-5 * time.Minute).UnixMilli()
		query := `
			SELECT count(*)
			FROM orders
			WHERE status IN (1, 2)
			AND updated_at < $1
		`
		var count int
		err := db.QueryRow(query, timeoutThreshold).Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 0, count, "Found %d stuck orders that have not been updated in the last 5 minutes. Possible matching engine or outbox failure.", count)
	})

	// 3. Trade / Order Consistency
	// 每張訂單的 filled_quantity 必須等於其所有 trades 的數量總和
	t.Run("Trade and Order Consistency", func(t *testing.T) {
		query := `
			SELECT SUM(discrepancy) FROM (
				SELECT 
					o.id, 
					CASE 
						WHEN ABS(o.filled_quantity - COALESCE(SUM(t.quantity), 0)) > 0.000000001 THEN 1 
						ELSE 0 
					END as discrepancy
				FROM orders o
				LEFT JOIN trades t ON (t.maker_order_id = o.id OR t.taker_order_id = o.id)
				WHERE o.filled_quantity > 0
				GROUP BY o.id, o.filled_quantity
			) subquery;
		`
		var sumDiscrepancy sql.NullInt64
		err := db.QueryRow(query).Scan(&sumDiscrepancy)
		assert.NoError(t, err)

		count := 0
		if sumDiscrepancy.Valid {
			count = int(sumDiscrepancy.Int64)
		}

		assert.Equal(t, 0, count, "Found %d orders where filled_quantity does not match trades. Critical accounting error.", count)
	})

	// 4. Outbox Consistency
	// 所有的 Outbox 事件都必須被成功發送至 Kafka
	t.Run("No Stuck Outbox Events", func(t *testing.T) {
		timeoutThreshold := time.Now().Add(-5 * time.Minute).UnixMilli()
		query := `
			SELECT count(*)
			FROM outbox_messages
			WHERE status = 0 -- 0: Pending
			AND created_at < $1
		`
		var count int
		err := db.QueryRow(query, timeoutThreshold).Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 0, count, "Found %d stuck outbox messages older than 5 minutes. Relay service might be down.", count)
	})

	// 5. Negative Balance Check
	// 檢查是否有任何帳戶出現負資產
	t.Run("No Negative Balances", func(t *testing.T) {
		query := `
			SELECT count(*)
			FROM accounts
			WHERE balance < 0 OR locked < 0
		`
		var count int
		err := db.QueryRow(query).Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 0, count, "Found %d accounts with negative balances or locked amounts. The system invariant is broken.", count)
	})
}
