package core

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DBTransaction 定義事務操作介面
type DBTransaction interface {
	ExecTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// OrderRepository defines the interface for order persistence
type OrderRepository interface {
	CreateOrder(ctx context.Context, order *Order) error
	GetOrder(ctx context.Context, id uuid.UUID) (*Order, error)
	UpdateOrder(ctx context.Context, order *Order) error
	GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error)
}

// TradeRepository defines the interface for trade persistence
type TradeRepository interface {
	CreateTrade(ctx context.Context, trade *matching.Trade) error
}

// AccountRepository defines the interface for account persistence
type AccountRepository interface {
	GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*Account, error)
	CreateAccount(ctx context.Context, account *Account) error
	UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
}

// UserRepository defines the interface for user persistence
type UserRepository interface {
	CreateUser(ctx context.Context, user *User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
}

// ExchangeService defines the core business logic
type ExchangeService interface {
	PlaceOrder(ctx context.Context, order *Order) error
	GetOrder(ctx context.Context, id uuid.UUID) (*Order, error)
	GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error)
	CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error
}
