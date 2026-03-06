package core

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type OrderSide string
type OrderType string
type OrderStatus string

const (
	SideBuy  OrderSide = "BUY"
	SideSell OrderSide = "SELL"

	TypeLimit  OrderType = "LIMIT"  // 限價單
	TypeMarket OrderType = "MARKET" // 市價單

	StatusNew             OrderStatus = "NEW"              // 新訂單
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED" // 部分成交
	StatusFilled          OrderStatus = "FILLED"           // 完全成交
	StatusCanceled        OrderStatus = "CANCELED"         // 已取消
	StatusRejected        OrderStatus = "REJECTED"         // 已拒絕
)

var (
	ErrInsufficientFunds = fmt.Errorf("insufficient funds")
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Account struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	Currency  string          `json:"currency"`   // 例如 "USD", "BTC"
	Balance   decimal.Decimal `json:"balance"`    // 可用餘額
	Locked    decimal.Decimal `json:"locked"`     // 鎖定餘額
	CreatedAt time.Time       `json:"created_at"` // 創建時間
	UpdatedAt time.Time       `json:"updated_at"` // 更新時間
}

type Order struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"user_id"`
	Symbol         string          `json:"symbol"`          // 例如 "BTCUSD"
	Side           OrderSide       `json:"side"`            // BUY 或 SELL
	Type           OrderType       `json:"type"`            // LIMIT 或 MARKET
	Price          decimal.Decimal `json:"price"`           // 價格，市價單可為0
	Quantity       decimal.Decimal `json:"quantity"`        // 訂單數量
	FilledQuantity decimal.Decimal `json:"filled_quantity"` // 已成交數量
	Status         OrderStatus     `json:"status"`          // 訂單狀態
	CreatedAt      time.Time       `json:"created_at"`      // 創建時間
	UpdatedAt      time.Time       `json:"updated_at"`      // 更新時間
}

type Trade struct {
	ID           uuid.UUID       `json:"id"`
	MakerOrderID uuid.UUID       `json:"maker_order_id"` // 被動訂單 ID
	TakerOrderID uuid.UUID       `json:"taker_order_id"` // 主動訂單 ID
	Symbol       string          `json:"symbol"`         // 例如 "BTCUSD"
	Price        decimal.Decimal `json:"price"`          // 成交價格
	Quantity     decimal.Decimal `json:"quantity"`       // 成交數量
	CreatedAt    time.Time       `json:"created_at"`     // 成交時間
}

type KLine struct {
	Timestamp time.Time       `json:"timestamp"` // K 線的開始時間
	Open      decimal.Decimal `json:"open"`      // 開盤價
	High      decimal.Decimal `json:"high"`      // 最高價
	Low       decimal.Decimal `json:"low"`       // 最低價
	Close     decimal.Decimal `json:"close"`     // 收盤價
	Volume    decimal.Decimal `json:"volume"`    // 成交量
}
