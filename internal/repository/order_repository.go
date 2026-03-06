package repository

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/google/uuid"
)

// --- OrderRepository Implementation ---

func (r *PostgresRepository) CreateOrder(ctx context.Context, order *core.Order) error {
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

func (r *PostgresRepository) GetOrder(ctx context.Context, id uuid.UUID) (*core.Order, error) {
	executor := r.getExecutor(ctx)
	query := `
		SELECT id, user_id, symbol, side, type, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE id = $1`

	row := executor.QueryRow(ctx, query, id)
	var o core.Order
	err := row.Scan(
		&o.ID, &o.UserID, &o.Symbol, &o.Side, &o.Type,
		&o.Price, &o.Quantity, &o.FilledQuantity, &o.Status,
		&o.CreatedAt, &o.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *PostgresRepository) UpdateOrder(ctx context.Context, order *core.Order) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE orders 
		SET filled_quantity = $1, status = $2, updated_at = $3
		WHERE id = $4`

	_, err := executor.Exec(ctx, query,
		order.FilledQuantity, order.Status, order.UpdatedAt, order.ID)
	return err
}

func (r *PostgresRepository) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*core.Order, error) {
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

	var orders []*core.Order
	for rows.Next() {
		var o core.Order
		err := rows.Scan(
			&o.ID, &o.UserID, &o.Symbol, &o.Side, &o.Type,
			&o.Price, &o.Quantity, &o.FilledQuantity, &o.Status,
			&o.CreatedAt, &o.UpdatedAt)
		if err != nil {
			return nil, err
		}
		orders = append(orders, &o)
	}
	return orders, nil
}

func (r *PostgresRepository) DeleteAllOrders(ctx context.Context) error {
	executor := r.getExecutor(ctx)
	_, err := executor.Exec(ctx, "DELETE FROM orders")
	return err
}
