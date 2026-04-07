package repository

import (
	"context"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// --- AccountRepository Implementation ---

func (r *PostgresRepository) GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*domain.Account, error) {
	executor := r.getExecutor(ctx)
	query := `SELECT user_id, currency, balance, locked, updated_at FROM accounts WHERE user_id = $1 AND currency = $2`

	row := executor.QueryRow(ctx, query, userID, currency)
	var acc domain.Account
	err := row.Scan(&acc.UserID, &acc.Currency, &acc.Balance, &acc.Locked, &acc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *PostgresRepository) CreateAccount(ctx context.Context, account *domain.Account) error {
	executor := r.getExecutor(ctx)
	query := `
		INSERT INTO accounts (id, user_id, currency, balance, locked, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, currency) DO NOTHING`

	_, err := executor.Exec(ctx, query,
		account.ID, account.UserID, account.Currency, account.Balance, account.Locked,
		account.CreatedAt, account.UpdatedAt)
	return err
}

func (r *PostgresRepository) UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE accounts 
		SET balance = balance + $1, updated_at = $4
		WHERE user_id = $2 AND currency = $3`

	_, err := executor.Exec(ctx, query, amount, userID, currency, time.Now().UnixMilli())
	return err
}

func (r *PostgresRepository) LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE accounts 
		SET balance = balance - $1, locked = locked + $1, updated_at = $4
		WHERE user_id = $2 AND currency = $3 AND balance >= $1`

	tag, err := executor.Exec(ctx, query, amount, userID, currency, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrInsufficientFunds
	}
	return nil
}

func (r *PostgresRepository) BatchLockFunds(ctx context.Context, lockedFunds map[uuid.UUID]map[string]decimal.Decimal) error {
	executor := r.getExecutor(ctx)

	// 擷取所有 UserID 並排序以避免行鎖死結 (Deadlock)
	userIDs := make([]uuid.UUID, 0, len(lockedFunds))
	for uid := range lockedFunds {
		userIDs = append(userIDs, uid)
	}
	// uuid 轉字串排序
	for i := 0; i < len(userIDs); i++ {
		for j := i + 1; j < len(userIDs); j++ {
			if userIDs[i].String() > userIDs[j].String() {
				userIDs[i], userIDs[j] = userIDs[j], userIDs[i]
			}
		}
	}

	now := time.Now().UnixMilli()

	for _, uid := range userIDs {
		currencies := lockedFunds[uid]
		curKeys := make([]string, 0, len(currencies))
		for cur := range currencies {
			curKeys = append(curKeys, cur)
		}
		// 幣種也排序
		for i := 0; i < len(curKeys); i++ {
			for j := i + 1; j < len(curKeys); j++ {
				if curKeys[i] > curKeys[j] {
					curKeys[i], curKeys[j] = curKeys[j], curKeys[i]
				}
			}
		}

		for _, cur := range curKeys {
			amount := currencies[cur]
			query := `
				UPDATE accounts 
				SET balance = balance - $1, locked = locked + $1, updated_at = $4
				WHERE user_id = $2 AND currency = $3 AND balance >= $1`
			tag, err := executor.Exec(ctx, query, amount, uid, cur, now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return domain.ErrInsufficientFunds
			}
		}
	}
	return nil
}

func (r *PostgresRepository) UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	executor := r.getExecutor(ctx)
	query := `
		UPDATE accounts 
		SET balance = balance + $1, locked = locked - $1, updated_at = $4
		WHERE user_id = $2 AND currency = $3 AND locked >= $1`

	_, err := executor.Exec(ctx, query, amount, userID, currency, time.Now().UnixMilli())
	return err
}

func (r *PostgresRepository) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error) {
	executor := r.getExecutor(ctx)
	query := `SELECT id, user_id, currency, balance, locked, created_at, updated_at FROM accounts WHERE user_id = $1`

	rows, err := executor.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*domain.Account
	for rows.Next() {
		var acc domain.Account
		if err := rows.Scan(&acc.ID, &acc.UserID, &acc.Currency, &acc.Balance, &acc.Locked, &acc.CreatedAt, &acc.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, &acc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}
