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

// OrderType 訂單類型
type OrderType string

const (
	TypeLimit  OrderType = "LIMIT"
	TypeMarket OrderType = "MARKET"
)

// Order 撮合引擎內部使用的訂單結構
type Order struct {
	ID       uuid.UUID
	Side     OrderSide
	Type     OrderType
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// NewOrder 建立新限價訂單
func NewOrder(side OrderSide, price, quantity decimal.Decimal) *Order {
	return &Order{
		ID:       uuid.New(),
		Side:     side,
		Type:     TypeLimit,
		Price:    price,
		Quantity: quantity,
	}
}

// NewMarketOrder 建立新市價訂單
func NewMarketOrder(side OrderSide, quantity decimal.Decimal) *Order {
	return &Order{
		ID:       uuid.New(),
		Side:     side,
		Type:     TypeMarket,
		Price:    decimal.Zero, // 市價單不指定價格
		Quantity: quantity,
	}
}

// Trade 成交記錄
type Trade struct {
	ID           uuid.UUID
	Symbol       string
	MakerOrderID uuid.UUID
	TakerOrderID uuid.UUID
	Price        decimal.Decimal
	Quantity     decimal.Decimal
}
