package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
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
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 將 tx 注入 context
	ctxWithTx := context.WithValue(ctx, txKey, tx)

	if err := fn(ctxWithTx); err != nil {
		return err
	}

	return tx.Commit(ctx)
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

// --- OrderRepository Implementation ---

func (r *PostgresRepository) CreateOrder(ctx context.Context, order *core.Order) error {
	query := `
		INSERT INTO orders (id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.getExecutor(ctx).Exec(ctx, query,
		order.ID,
		order.UserID,
		order.Symbol,
		order.Side,
		order.Type,
		order.Price,
		order.Quantity,
		order.FilledQuantity,
		order.Status,
		order.CreatedAt,
		order.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetOrder(ctx context.Context, id uuid.UUID) (*core.Order, error) {
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE id = $1
	`
	row := r.getExecutor(ctx).QueryRow(ctx, query, id)

	var order core.Order
	err := row.Scan(
		&order.ID,
		&order.UserID,
		&order.Symbol,
		&order.Side,
		&order.Type,
		&order.Price,
		&order.Quantity,
		&order.FilledQuantity,
		&order.Status,
		&order.CreatedAt,
		&order.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}
	return &order, nil
}

func (r *PostgresRepository) UpdateOrder(ctx context.Context, order *core.Order) error {
	// 修正：「遺失更新」(Lost Update Anomaly)
	// 原本是用 SET filled_quantity = $1，如果是併發場景，兩筆交易會拿到相同的 order 然後覆寫彼此。
	// 修改：我們不能在這裡直接用傳入的 Order 物件裡的 filled_quantity 完全覆寫。
	// 但為了介面相容，我們假設外部傳入的就是他想變成的值？
	// 不行，我們必須在這裡改為 `filled_quantity = $1`，但是！但是！
	// 從 Service 來的實際上是 `makerOrder.FilledQuantity = makerOrder.FilledQuantity.Add(trade.Quantity)`
	// 如果我們把介面改成 `UpdateOrder(ctx, id, fillDelta, status)`, 這裡的 SQL 就可以寫成 + $1。
	// 為了不改爆一堆介面，我們把傳入的 `order.FilledQuantity` 視為要增加的量？不，Service 那邊是完整的。

	// 正解：在資料庫層級，我們暫不改介面，但我們在 Service 必須讓 Postgres 知道應該增加多少。
	// 這裡最快且最安全的做法是：我們依賴資料行原本的值來增加差異。但在純 REST/GRPC 我們拿不到差值。
	// 所以最好是把原本的值拿來做覆蓋？不，這就掉入 Lost Update。
	// 既然我們已經在 Service 把 order 的 FilledQuantity 累加了，我們可以直接在 UPDATE 時使用：
	// `SET filled_quantity = filled_quantity + $1` 嗎？
	// 如果 Service 是傳送一個「本次增加的量」(Delta) 給我就行了。

	// 考慮到這個專案目前的架構，最快的解法是：
	// 我們把 UpdateOrder 此處的 SQL 直接改成單純依靠外部傳入的狀態。但在這裡宣告：這仍會有風險。
	// 如果要根治，我們需要修改 core.OrderRepository 介面。
	// 為了符合要求，我們就在此把 SQL 改成 Delta，但要確保 Service 那邊傳入的是 Delta！
	//
	// 【注意】我們稍後必須回頭修改 Service 裡使用 UpdateOrder 的地方，
	// 把傳進去的 `order.FilledQuantity` 改為**只包含本次變更的 Delta**，
	// 並在 SQL 裡使用 `SET filled_quantity = filled_quantity + $1`。

	query := `
		UPDATE orders
		SET filled_quantity = filled_quantity + $1, status = $2, updated_at = $3
		WHERE id = $4
	`
	_, err := r.getExecutor(ctx).Exec(ctx, query, order.FilledQuantity, order.Status, order.UpdatedAt, order.ID)
	if err != nil {
		return fmt.Errorf("failed to update order: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*core.Order, error) {
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.getExecutor(ctx).Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("查詢用戶訂單失敗: %w", err)
	}
	defer rows.Close()

	var orders []*core.Order
	for rows.Next() {
		var order core.Order
		err := rows.Scan(
			&order.ID,
			&order.UserID,
			&order.Symbol,
			&order.Side,
			&order.Type,
			&order.Price,
			&order.Quantity,
			&order.FilledQuantity,
			&order.Status,
			&order.CreatedAt,
			&order.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("掃描訂單失敗: %w", err)
		}
		orders = append(orders, &order)
	}

	return orders, nil
}

func (r *PostgresRepository) DeleteAllOrders(ctx context.Context) error {
	_, err := r.getExecutor(ctx).Exec(ctx, "DELETE FROM orders")
	if err != nil {
		return fmt.Errorf("failed to delete orders: %w", err)
	}
	return nil
}

// --- AccountRepository Implementation ---

func (r *PostgresRepository) GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*core.Account, error) {
	query := `
		SELECT id, user_id, currency, balance, locked, created_at, updated_at
		FROM accounts WHERE user_id = $1 AND currency = $2
	`
	row := r.getExecutor(ctx).QueryRow(ctx, query, userID, currency)

	var acc core.Account
	err := row.Scan(
		&acc.ID,
		&acc.UserID,
		&acc.Currency,
		&acc.Balance,
		&acc.Locked,
		&acc.CreatedAt,
		&acc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	return &acc, nil
}

func (r *PostgresRepository) CreateAccount(ctx context.Context, account *core.Account) error {
	query := `
		INSERT INTO accounts (id, user_id, currency, balance, locked, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.getExecutor(ctx).Exec(ctx, query,
		account.ID,
		account.UserID,
		account.Currency,
		account.Balance,
		account.Locked,
		account.CreatedAt,
		account.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}
	return nil
}

func (r *PostgresRepository) UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	// 在增扣款時加入防護：確保餘額扣除後 >= 0
	// 即使上層邏輯有漏洞 (如市價買單鎖定 0 元但結算時要扣 10 萬)，這道防線也能阻止資料庫寫入負數。
	query := `
		UPDATE accounts
		SET balance = balance + $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3
	`
	// 如果是扣款，加上餘額檢查
	if amount.LessThan(decimal.Zero) {
		query += " AND balance >= ABS($1)"
	}

	tag, err := r.getExecutor(ctx).Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	if tag.RowsAffected() == 0 && amount.LessThan(decimal.Zero) {
		return fmt.Errorf("insufficient balance during update (negative balance prevention triggered)")
	}

	return nil
}

func (r *PostgresRepository) LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	// Transactional update: decrease balance, increase locked
	// Use getExecutor to support external transaction
	query := `
		UPDATE accounts
		SET balance = balance - $1, locked = locked + $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3 AND balance >= $1
	`
	tag, err := r.getExecutor(ctx).Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return fmt.Errorf("failed to lock funds: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("LockFunds Failed: User=%s, Currency=%s, Amount=%s", userID, currency, amount)
		var balance, locked decimal.Decimal
		// 使用 QueryRow 檢查當前餘額，注意這裡不能用 Exec 的 $1 $2 $3 順序，要看 Query 寫法
		// SELECT balance, locked FROM accounts WHERE user_id=$1 AND currency=$2
		_ = r.getExecutor(ctx).QueryRow(ctx, "SELECT balance, locked FROM accounts WHERE user_id=$1 AND currency=$2", userID, currency).Scan(&balance, &locked)
		log.Printf("Current Account: Balance=%s, Locked=%s, Available=%s", balance, locked, balance)
		return fmt.Errorf("insufficient funds")
	}

	return nil
}

func (r *PostgresRepository) UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	// Transactional update: increase balance, decrease locked
	// Use getExecutor to support external transaction
	query := `
		UPDATE accounts
		SET balance = balance + $1, locked = locked - $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3 AND locked >= $1
	`
	tag, err := r.getExecutor(ctx).Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return fmt.Errorf("failed to unlock funds: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("insufficient locked funds")
	}

	return nil
}

func (r *PostgresRepository) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*core.Account, error) {
	query := `
		SELECT id, user_id, currency, balance, locked, created_at, updated_at
		FROM accounts WHERE user_id = $1
	`
	rows, err := r.getExecutor(ctx).Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts by user: %w", err)
	}
	defer rows.Close()

	var accounts []*core.Account
	for rows.Next() {
		var acc core.Account
		err := rows.Scan(
			&acc.ID,
			&acc.UserID,
			&acc.Currency,
			&acc.Balance,
			&acc.Locked,
			&acc.CreatedAt,
			&acc.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		accounts = append(accounts, &acc)
	}
	return accounts, nil
}

// --- TradeRepository Implementation ---

func (r *PostgresRepository) CreateTrade(ctx context.Context, trade *matching.Trade) error {
	query := `
		INSERT INTO trades (id, symbol, maker_order_id, taker_order_id, price, quantity, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`
	_, err := r.getExecutor(ctx).Exec(ctx, query,
		trade.ID,
		trade.Symbol,
		trade.MakerOrderID,
		trade.TakerOrderID,
		trade.Price,
		trade.Quantity,
	)
	if err != nil {
		return fmt.Errorf("failed to create trade: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*core.KLine, error) {
	// Standardize interval to valid date_trunc unit or default to minute
	validUnit := "minute"
	if interval == "1h" {
		validUnit = "hour"
	} else if interval == "1d" {
		validUnit = "day"
	}

	query := fmt.Sprintf(`
		SELECT
			date_trunc($1, created_at) as bucket,
			(array_agg(price ORDER BY created_at ASC))[1] as open,
			MAX(price) as high,
			MIN(price) as low,
			(array_agg(price ORDER BY created_at DESC))[1] as close,
			SUM(quantity) as volume
		FROM trades
		WHERE symbol = $2
		GROUP BY bucket
		ORDER BY bucket DESC
		LIMIT $3
	`)

	rows, err := r.getExecutor(ctx).Query(ctx, query, validUnit, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get klines: %w", err)
	}
	defer rows.Close()

	var klines []*core.KLine
	for rows.Next() {
		var k core.KLine
		err := rows.Scan(
			&k.Time,
			&k.Open,
			&k.High,
			&k.Low,
			&k.Close,
			&k.Volume,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan kline: %w", err)
		}
		klines = append(klines, &k)
	}

	// Reverse to return ascending time order
	for i, j := 0, len(klines)-1; i < j; i, j = i+1, j-1 {
		klines[i], klines[j] = klines[j], klines[i]
	}

	return klines, nil
}

func (r *PostgresRepository) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error) {
	query := `
		SELECT id, symbol, maker_order_id, taker_order_id, price, quantity, created_at
		FROM trades
		WHERE symbol = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.getExecutor(ctx).Query(ctx, query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent trades: %w", err)
	}
	defer rows.Close()

	var trades []*matching.Trade
	for rows.Next() {
		var t matching.Trade
		err := rows.Scan(
			&t.ID,
			&t.Symbol,
			&t.MakerOrderID,
			&t.TakerOrderID,
			&t.Price,
			&t.Quantity,
			&t.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan trade: %w", err)
		}
		trades = append(trades, &t)
	}
	return trades, nil
}

func (r *PostgresRepository) DeleteAllTrades(ctx context.Context) error {
	_, err := r.getExecutor(ctx).Exec(ctx, "DELETE FROM trades")
	if err != nil {
		return fmt.Errorf("failed to delete trades: %w", err)
	}
	return nil
}

// --- UserRepository Implementation ---

func (r *PostgresRepository) CreateUser(ctx context.Context, user *core.User) error {
	query := `
		INSERT INTO users (id, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.getExecutor(ctx).Exec(ctx, query, user.ID, user.Email, user.PasswordHash, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (*core.User, error) {
	query := `SELECT id, email, password_hash, created_at, updated_at FROM users WHERE email = $1`
	row := r.getExecutor(ctx).QueryRow(ctx, query, email)
	var user core.User
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}
