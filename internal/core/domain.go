package core

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// OrderSide 訂單方向 (SMALLINT: 1=BUY, 2=SELL)
type OrderSide int16

// OrderType 訂單類型 (SMALLINT: 1=LIMIT, 2=MARKET)
type OrderType int16

// OrderStatus 訂單狀態 (SMALLINT: 1=NEW, 2=PARTIALLY_FILLED, 3=FILLED, 4=CANCELED, 5=REJECTED)
type OrderStatus int16

const (
	SideBuy  OrderSide = 1
	SideSell OrderSide = 2

	TypeLimit  OrderType = 1 // 限價單
	TypeMarket OrderType = 2 // 市價單

	StatusNew             OrderStatus = 1 // 新訂單
	StatusPartiallyFilled OrderStatus = 2 // 部分成交
	StatusFilled          OrderStatus = 3 // 完全成交
	StatusCanceled        OrderStatus = 4 // 已取消
	StatusRejected        OrderStatus = 5 // 已拒絕
)

// SideFromString 字串轉 OrderSide (API 輸入層使用)
func SideFromString(s string) (OrderSide, error) {
	switch s {
	case "BUY":
		return SideBuy, nil
	case "SELL":
		return SideSell, nil
	default:
		return 0, fmt.Errorf("無效的訂單方向: %s", s)
	}
}

// TypeFromString 字串轉 OrderType (API 輸入層使用)
func TypeFromString(s string) (OrderType, error) {
	switch s {
	case "LIMIT":
		return TypeLimit, nil
	case "MARKET":
		return TypeMarket, nil
	default:
		return 0, fmt.Errorf("無效的訂單類型: %s", s)
	}
}

// SideToString OrderSide 轉字串 (API 輸出層使用)
func SideToString(s OrderSide) string {
	switch s {
	case SideBuy:
		return "BUY"
	case SideSell:
		return "SELL"
	default:
		return "UNKNOWN"
	}
}

// TypeToString OrderType 轉字串 (API 輸出層使用)
func TypeToString(t OrderType) string {
	switch t {
	case TypeLimit:
		return "LIMIT"
	case TypeMarket:
		return "MARKET"
	default:
		return "UNKNOWN"
	}
}

// StatusToString OrderStatus 轉字串 (API 輸出層使用)
func StatusToString(s OrderStatus) string {
	switch s {
	case StatusNew:
		return "NEW"
	case StatusPartiallyFilled:
		return "PARTIALLY_FILLED"
	case StatusFilled:
		return "FILLED"
	case StatusCanceled:
		return "CANCELED"
	case StatusRejected:
		return "REJECTED"
	default:
		return "UNKNOWN"
	}
}

var (
	ErrInsufficientFunds = fmt.Errorf("insufficient funds")
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    int64     `json:"created_at"` // Unix 毫秒
	UpdatedAt    int64     `json:"updated_at"` // Unix 毫秒
}

type Account struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	Currency  string          `json:"currency"`   // 例如 "USD", "BTC"
	Balance   decimal.Decimal `json:"balance"`    // 可用餘額
	Locked    decimal.Decimal `json:"locked"`     // 鎖定餘額
	CreatedAt int64           `json:"created_at"` // Unix 毫秒
	UpdatedAt int64           `json:"updated_at"` // Unix 毫秒
}

type Order struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"user_id"`
	Symbol         string          `json:"symbol"`          // 例如 "BTC-USD"
	Side           OrderSide       `json:"side"`            // 1=BUY, 2=SELL
	Type           OrderType       `json:"type"`            // 1=LIMIT, 2=MARKET
	Price          decimal.Decimal `json:"price"`           // 價格，市價單為 0
	Quantity       decimal.Decimal `json:"quantity"`        // 訂單數量
	FilledQuantity decimal.Decimal `json:"filled_quantity"` // 已成交數量
	Status         OrderStatus     `json:"status"`          // 訂單狀態
	CreatedAt      int64           `json:"created_at"`      // Unix 毫秒
	UpdatedAt      int64           `json:"updated_at"`      // Unix 毫秒
}

type KLine struct {
	Timestamp int64           `json:"timestamp"` // K 線開始的 Unix 毫秒
	Open      decimal.Decimal `json:"open"`      // 開盤價
	High      decimal.Decimal `json:"high"`      // 最高價
	Low       decimal.Decimal `json:"low"`       // 最低價
	Close     decimal.Decimal `json:"close"`     // 收盤價
	Volume    decimal.Decimal `json:"volume"`    // 成交量
}
