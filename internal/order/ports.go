package order

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DBTransaction 定義事務操作介面
//
// ExecTx 開啟一個資料庫事務，並將 pgx.Tx 注入 Context。
// 所有在 fn 內部呼叫的 Repository 方法都會自動感知並使用同一個 Tx。
//
// ValidateFencingTokenTx 也必須在 fn 閉包的 Context 下呼叫，
// 確保 Token 驗證與後續的 UPDATE 帳戶餘額在同一個 Tx 內原子執行。
type DBTransaction interface {
	ExecTx(ctx context.Context, fn func(ctx context.Context) error) error

	// ValidateFencingTokenTx 在目前的資料庫事務內驗證 FencingToken 是否合法。
	// 關鍵：使用 FOR SHARE 行鎖，阻止其他事務（新 Leader 上位）在此期間
	// 修改 partition_leader_locks，形成一道原子性「護城河」。
	// token <= 0 時直接通過（向後相容）。
	// 若 token < DB 中的 current token，代表是殭屍訊息，回傳 false。
	ValidateFencingTokenTx(ctx context.Context, partition string, token int64) (valid bool, err error)
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
