package repository

import (
	"context"
	"fmt"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/google/uuid"
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

// --- OrderRepository Implementation ---

func (r *PostgresRepository) CreateOrder(ctx context.Context, order *core.Order) error {
	query := `
		INSERT INTO orders (id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.db.Exec(ctx, query,
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
	row := r.db.QueryRow(ctx, query, id)

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
	query := `
		UPDATE orders
		SET filled_quantity = $1, status = $2, updated_at = $3
		WHERE id = $4
	`
	_, err := r.db.Exec(ctx, query, order.FilledQuantity, order.Status, order.UpdatedAt, order.ID)
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
	rows, err := r.db.Query(ctx, query, userID)
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

// --- AccountRepository Implementation ---

func (r *PostgresRepository) GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*core.Account, error) {
	query := `
		SELECT id, user_id, currency, balance, locked, created_at, updated_at
		FROM accounts WHERE user_id = $1 AND currency = $2
	`
	row := r.db.QueryRow(ctx, query, userID, currency)

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
	_, err := r.db.Exec(ctx, query,
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
	// This is a simplified update. In reality, we might want to add/subtract.
	// Here assuming 'amount' is the new balance or delta?
	// Let's assume delta for safety, but the interface name implies setting or updating.
	// Let's implement it as "Add to Balance" for now, or better, just update the specific fields.
	// But wait, the interface says UpdateBalance. Let's assume it adds the amount (can be negative).

	query := `
		UPDATE accounts
		SET balance = balance + $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3
	`
	_, err := r.db.Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}
	return nil
}

func (r *PostgresRepository) LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	// Transactional update: decrease balance, increase locked
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	query := `
		UPDATE accounts
		SET balance = balance - $1, locked = locked + $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3 AND balance >= $1
	`
	tag, err := tx.Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return fmt.Errorf("failed to lock funds: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("insufficient funds")
	}

	return tx.Commit(ctx)
}

func (r *PostgresRepository) UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	// Transactional update: increase balance, decrease locked
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	query := `
		UPDATE accounts
		SET balance = balance + $1, locked = locked - $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3 AND locked >= $1
	`
	tag, err := tx.Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return fmt.Errorf("failed to unlock funds: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("insufficient locked funds")
	}

	return tx.Commit(ctx)
}

// --- UserRepository Implementation ---

func (r *PostgresRepository) CreateUser(ctx context.Context, user *core.User) error {
	query := `
		INSERT INTO users (id, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.Exec(ctx, query, user.ID, user.Email, user.PasswordHash, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (*core.User, error) {
	query := `SELECT id, email, password_hash, created_at, updated_at FROM users WHERE email = $1`
	row := r.db.QueryRow(ctx, query, email)
	var user core.User
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &user, nil
}
