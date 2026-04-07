package repository

import (
	"context"
	"fmt"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- OrderRepository Implementation ---

func (r *PostgresRepository) CreateOrder(ctx context.Context, order *domain.Order) error {
	executor := r.getExecutor(ctx)
	query := `
		INSERT INTO orders (id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := executor.Exec(ctx, query,
		order.ID, order.UserID, order.Symbol, order.Side, order.Type,
		order.Price, order.Quantity, order.FilledQuantity, order.Status,
		order.CreatedAt, order.UpdatedAt)
	return err
}

func (r *PostgresRepository) BatchCreateOrders(ctx context.Context, orders []*domain.Order) error {
	if len(orders) == 0 {
		return nil
	}
	
	tx := db.GetTx(ctx)
	if tx == nil {
		return fmt.Errorf("BatchCreateOrders must be called within ExecTx")
	}

	rows := make([][]any, 0, len(orders))
	for _, order := range orders {
		rows = append(rows, []any{
			order.ID, order.UserID, order.Symbol, order.Side, order.Type,
			order.Price, order.Quantity, order.FilledQuantity, order.Status,
			order.CreatedAt, order.UpdatedAt,
		})
	}

	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"orders"},
		[]string{"id", "user_id", "symbol", "side", "type", "price", "quantity", "filled_quantity", "status", "created_at", "updated_at"},
		pgx.CopyFromRows(rows),
	)
	return err
}

func (r *PostgresRepository) GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	executor := r.getExecutor(ctx)
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE id = $1`

	row := executor.QueryRow(ctx, query, id)
	return scanOrder(row)
}

// GetOrderForUpdate 加上 FOR UPDATE 悲觀鎖，防止 CancelOrder 與 ProcessTrade 發生競態條件
// 必須在事務 (ExecTx) 內部呼叫才有效
func (r *PostgresRepository) GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	executor := r.getExecutor(ctx)
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE id = $1
		FOR UPDATE`

	row := executor.QueryRow(ctx, query, id)
	return scanOrder(row)
}

func (r *PostgresRepository) UpdateOrder(ctx context.Context, order *domain.Order) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE orders 
		SET filled_quantity = $1, status = $2, updated_at = $3
		WHERE id = $4`

	_, err := executor.Exec(ctx, query,
		order.FilledQuantity, order.Status, order.UpdatedAt, order.ID)
	return err
}

func (r *PostgresRepository) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Order, error) {
	executor := r.getExecutor(ctx)
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE user_id = $1
		ORDER BY created_at DESC`

	rows, err := executor.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

func (r *PostgresRepository) GetActiveOrders(ctx context.Context) ([]*domain.Order, error) {
	executor := r.getExecutor(ctx)
	// Status 1=NEW, 2=PARTIALLY_FILLED, Type 1=LIMIT
	// 只恢復限價單，市價單不應該存入掛單簿
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders 
		WHERE status IN (1, 2) AND type = 1
		ORDER BY created_at ASC`

	rows, err := executor.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

func (r *PostgresRepository) DeleteAllOrders(ctx context.Context) error {
	executor := r.getExecutor(ctx)
	_, err := executor.Exec(ctx, "DELETE FROM orders")
	return err
}

// scanOrder 共用掃描邏輯，減少重複程式碼
type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(
		&o.ID, &o.UserID, &o.Symbol, &o.Side, &o.Type,
		&o.Price, &o.Quantity, &o.FilledQuantity, &o.Status,
		&o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}
