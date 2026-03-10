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

// TradeEventListener defines the interface for trade events
type TradeEventListener interface {
	OnTrade(trade *matching.Trade)
	OnOrderUpdate(order *Order)                          // 推播訂單狀態更新
	OnOrderBookUpdate(snapshot *matching.OrderBookSnapshot) // 推播掛單簿深度快照
}

// OrderRepository defines the interface for order persistence
type OrderRepository interface {
	CreateOrder(ctx context.Context, order *Order) error
	GetOrder(ctx context.Context, id uuid.UUID) (*Order, error)
	GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*Order, error) // 加悲觀鎖（FOR UPDATE）
	UpdateOrder(ctx context.Context, order *Order) error
	GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error)
	GetActiveOrders(ctx context.Context) ([]*Order, error) // 取得所有尚未完全成交的訂單
	DeleteAllOrders(ctx context.Context) error
}

// TradeRepository defines the interface for trade persistence
type TradeRepository interface {
	CreateTrade(ctx context.Context, trade *matching.Trade) error
	GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*KLine, error)
	GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error)
	DeleteAllTrades(ctx context.Context) error
}

// AccountRepository defines the interface for account persistence
type AccountRepository interface {
	GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*Account, error)
	CreateAccount(ctx context.Context, account *Account) error
	UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*Account, error)
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
	GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error)
	RegisterAnonymousUser(ctx context.Context) (*User, []*Account, error)
	GetBalances(ctx context.Context, userID uuid.UUID) ([]*Account, error)
	GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*KLine, error)
	GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error)
	ClearSimulationData(ctx context.Context) error
	RechargeTestUser(ctx context.Context, userID uuid.UUID) error
	RestoreEngineSnapshot(ctx context.Context) error // 伺服器啟動時，重現引擎狀態
}
