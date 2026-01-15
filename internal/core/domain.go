package core

import (
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

	TypeLimit  OrderType = "LIMIT"
	TypeMarket OrderType = "MARKET"

	StatusNew             OrderStatus = "NEW"
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	StatusFilled          OrderStatus = "FILLED"
	StatusCanceled        OrderStatus = "CANCELED"
	StatusRejected        OrderStatus = "REJECTED"
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
	Currency  string          `json:"currency"`
	Balance   decimal.Decimal `json:"balance"`
	Locked    decimal.Decimal `json:"locked"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type Order struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"user_id"`
	Symbol         string          `json:"symbol"`
	Side           OrderSide       `json:"side"`
	Type           OrderType       `json:"type"`
	Price          decimal.Decimal `json:"price"`
	Quantity       decimal.Decimal `json:"quantity"`
	FilledQuantity decimal.Decimal `json:"filled_quantity"`
	Status         OrderStatus     `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Trade struct {
	ID           uuid.UUID       `json:"id"`
	MakerOrderID uuid.UUID       `json:"maker_order_id"`
	TakerOrderID uuid.UUID       `json:"taker_order_id"`
	Symbol       string          `json:"symbol"`
	Price        decimal.Decimal `json:"price"`
	Quantity     decimal.Decimal `json:"quantity"`
	CreatedAt    time.Time       `json:"created_at"`
}
