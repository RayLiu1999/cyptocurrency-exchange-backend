package matching

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// OrderSide 訂單方向
type OrderSide string

const (
	SideBuy  OrderSide = "BUY"
	SideSell OrderSide = "SELL"
)

// Order 撮合引擎內部使用的訂單結構
type Order struct {
	ID       uuid.UUID
	Side     OrderSide
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// NewOrder 建立新訂單
func NewOrder(side OrderSide, price, quantity decimal.Decimal) *Order {
	return &Order{
		ID:       uuid.New(),
		Side:     side,
		Price:    price,
		Quantity: quantity,
	}
}

// Trade 成交記錄
type Trade struct {
	ID           uuid.UUID
	MakerOrderID uuid.UUID
	TakerOrderID uuid.UUID
	Price        decimal.Decimal
	Quantity     decimal.Decimal
}
