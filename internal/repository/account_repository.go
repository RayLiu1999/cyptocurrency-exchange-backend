package repository

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// --- AccountRepository Implementation ---

func (r *PostgresRepository) GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*core.Account, error) {
	executor := r.getExecutor(ctx)
	query := `SELECT user_id, currency, balance, locked, updated_at FROM accounts WHERE user_id = $1 AND currency = $2`

	row := executor.QueryRow(ctx, query, userID, currency)
	var acc core.Account
	err := row.Scan(&acc.UserID, &acc.Currency, &acc.Balance, &acc.Locked, &acc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *PostgresRepository) CreateAccount(ctx context.Context, account *core.Account) error {
	executor := r.getExecutor(ctx)
	query := `
		INSERT INTO accounts (user_id, currency, balance, locked, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, currency) DO NOTHING`

	_, err := executor.Exec(ctx, query,
		account.UserID, account.Currency, account.Balance, account.Locked, account.UpdatedAt)
	return err
}

func (r *PostgresRepository) UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE accounts 
		SET balance = balance + $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3`

	_, err := executor.Exec(ctx, query, amount, userID, currency)
	return err
}

func (r *PostgresRepository) LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE accounts 
		SET balance = balance - $1, locked = locked + $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3 AND balance >= $1`

	tag, err := executor.Exec(ctx, query, amount, userID, currency)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return core.ErrInsufficientFunds
	}
	return nil
}

func (r *PostgresRepository) UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE accounts 
		SET locked = locked - $1, updated_at = NOW()
		WHERE user_id = $2 AND currency = $3 AND locked >= $1`

	_, err := executor.Exec(ctx, query, amount, userID, currency)
	return err
}

func (r *PostgresRepository) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*core.Account, error) {
	executor := r.getExecutor(ctx)
	query := `SELECT user_id, currency, balance, locked, updated_at FROM accounts WHERE user_id = $1`

	rows, err := executor.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*core.Account
	for rows.Next() {
		var acc core.Account
		if err := rows.Scan(&acc.UserID, &acc.Currency, &acc.Balance, &acc.Locked, &acc.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, &acc)
	}
	return accounts, nil
}
