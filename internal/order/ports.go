package order

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DBTransaction 定義事務操作介面
type DBTransaction interface {
	ExecTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// OrderRepository defines the interface for order persistence
type OrderRepository interface {
	CreateOrder(ctx context.Context, order *domain.Order) error
	GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error)
	GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*domain.Order, error) // 加悲觀鎖（FOR UPDATE）
	UpdateOrder(ctx context.Context, order *domain.Order) error
	GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Order, error)
	GetActiveOrders(ctx context.Context) ([]*domain.Order, error) // 取得所有尚未完全成交的訂單
	DeleteAllOrders(ctx context.Context) error
}

// TradeRepository defines the interface for trade persistence
type TradeRepository interface {
	CreateTrade(ctx context.Context, trade *engine.Trade) error
	TradeExistsByID(ctx context.Context, id uuid.UUID) (bool, error) // 冪等性檢查
}

// AccountRepository defines the interface for account persistence
type AccountRepository interface {
	GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*domain.Account, error)
	CreateAccount(ctx context.Context, account *domain.Account) error
	UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error
	GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error)
}

// UserRepository defines the interface for user persistence
type UserRepository interface {
	CreateUser(ctx context.Context, user *domain.User) error
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
}

// OrderService 定義訂單與帳戶服務介面
type OrderService interface {
	PlaceOrder(ctx context.Context, order *domain.Order) error
	GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error)
	GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Order, error)
	CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error
	RegisterAnonymousUser(ctx context.Context) (*domain.User, []*domain.Account, error)
	GetBalances(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error)
	RechargeTestUser(ctx context.Context, userID uuid.UUID) error
}
