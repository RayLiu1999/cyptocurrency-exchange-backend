package core

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// RegisterAnonymousUser 建立匿名用戶並發放測試金
func (s *ExchangeServiceImpl) RegisterAnonymousUser(ctx context.Context) (*User, []*Account, error) {
	newUserID := uuid.New()
	now := time.Now()

	user := &User{
		ID:           newUserID,
		Email:        fmt.Sprintf("anonymous_%s@test.com", newUserID.String()[:8]),
		PasswordHash: "N/A",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	currencies := []struct {
		code   string
		amount decimal.Decimal
	}{
		{"USD", decimal.NewFromInt(1000000)},
		{"BTC", decimal.NewFromInt(100)},
		{"ETH", decimal.NewFromInt(1000)},
	}

	var accounts []*Account

	err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if err := s.userRepo.CreateUser(ctx, user); err != nil {
			return fmt.Errorf("建立用戶失敗: %w", err)
		}

		for _, c := range currencies {
			acc := &Account{
				ID:        uuid.New(),
				UserID:    newUserID,
				Currency:  c.code,
				Balance:   c.amount,
				Locked:    decimal.Zero,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := s.accountRepo.CreateAccount(ctx, acc); err != nil {
				return fmt.Errorf("建立帳戶 %s 失敗: %w", c.code, err)
			}
			accounts = append(accounts, acc)
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return user, accounts, nil
}

func (s *ExchangeServiceImpl) GetBalances(ctx context.Context, userID uuid.UUID) ([]*Account, error) {
	return s.accountRepo.GetAccountsByUser(ctx, userID)
}
