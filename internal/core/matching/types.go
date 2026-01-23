package matching

import (
	"time"

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
	ID           uuid.UUID       `json:"id"`
	Symbol       string          `json:"symbol"`
	MakerOrderID uuid.UUID       `json:"maker_order_id"`
	TakerOrderID uuid.UUID       `json:"taker_order_id"`
	Price        decimal.Decimal `json:"price"`
	Quantity     decimal.Decimal `json:"quantity"`
	CreatedAt    time.Time       `json:"created_at"`
}

// OrderBookLevel 訂單簿深度層級
type OrderBookLevel struct {
	Price    decimal.Decimal `json:"price"`
	Quantity decimal.Decimal `json:"quantity"`
}

// OrderBookSnapshot 訂單簿快照 (用於 API 回傳)
type OrderBookSnapshot struct {
	Symbol string           `json:"symbol"`
	Bids   []OrderBookLevel `json:"bids"` // 買單 (Price DESC)
	Asks   []OrderBookLevel `json:"asks"` // 賣單 (Price ASC)
}

func NewOrderBookSnapshot(symbol string) *OrderBookSnapshot {
	return &OrderBookSnapshot{
		Symbol: symbol,
		Bids:   make([]OrderBookLevel, 0),
		Asks:   make([]OrderBookLevel, 0),
	}
}
