package core

import (
	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// --- Kafka Topic 常數 ---
// 定義在 Core 層，確保 Core 不依賴 Infrastructure

const (
	TopicOrders      = "exchange.orders"      // 下單 & 撤單命令 (Partition Key = symbol)
	TopicSettlements = "exchange.settlements" // 撮合完成後的結算請求
	TopicTrades      = "exchange.trades"      // 個別成交事件（供外部訂閱）
	TopicOrderBook   = "exchange.orderbook"   // 掜單簿快照更新
)

// --- EventType 常數 ---

// EventType 事件類型標識
type EventType string

const (
	EventOrderPlaced          EventType = "order.placed"           // 下單成功，已鎖定資金
	EventOrderCancelRequested EventType = "order.cancel_requested" // 撤單請求，已標記 CANCELED 且資金已解鎖
	EventSettlementRequested  EventType = "settlement.requested"   // 撮合完成，等待結算（包含無成交的市價單退款）
	EventTradeExecuted        EventType = "trade.executed"         // 個別成交事件（供 WebSocket 推播與外部訂閱）
	EventOrderBookUpdated     EventType = "orderbook.updated"      // 掜單簿快照更新事件（撮合完成或撤單後觸發）
)

// --- 事件結構體 ---

// OrderPlacedEvent TX1 完成後發布：訂單建立並成功鎖定資金
// Partition Key: Symbol（確保同一交易對的下單/撤單嚴格有序）
type OrderPlacedEvent struct {
	EventType      EventType       `json:"event_type"`
	Symbol         string          `json:"symbol"`
	OrderID        uuid.UUID       `json:"order_id"`
	UserID         uuid.UUID       `json:"user_id"`
	Side           OrderSide       `json:"side"`
	Type           OrderType       `json:"type"`
	Price          decimal.Decimal `json:"price"`
	Quantity       decimal.Decimal `json:"quantity"`
	CreatedAt      int64           `json:"created_at"`      // Unix 毫秒
	AmountLocked   decimal.Decimal `json:"amount_locked"`   // TX1 鎖定的金額（供 TX2 計算退款）
	LockedCurrency string          `json:"locked_currency"` // 鎖定的幣種
}

// OrderCancelRequestedEvent DB TX 成功後發布：訂單已在 DB 標記 CANCELED、資金已解鎖
// Partition Key: Symbol（與 OrderPlacedEvent 共用同一 Partition，確保引擎操作有序）
// ⚠️ 重要：必須與 OrderPlacedEvent 走同一 Topic (exchange.orders)、同一 Partition (symbol key)
// 這樣 matching_consumer 才能保證「先處理下單、再處理撤單」，不會出現撤一個不存在的訂單
type OrderCancelRequestedEvent struct {
	EventType EventType `json:"event_type"`
	Symbol    string    `json:"symbol"`
	OrderID   uuid.UUID `json:"order_id"`
	Side      OrderSide `json:"side"`
}

// SettlementRequestedEvent matching_consumer 撮合完成後發布，攜帶全部成交資訊供 TX2 結算
// 一個 OrderPlacedEvent 對應一個 SettlementRequestedEvent（即使無成交也發布，處理市價單退款 or 狀態更新）
type SettlementRequestedEvent struct {
	EventType      EventType         `json:"event_type"`
	Symbol         string            `json:"symbol"`
	TakerOrderID   uuid.UUID         `json:"taker_order_id"`
	AmountLocked   decimal.Decimal   `json:"amount_locked"`   // 從 OrderPlacedEvent 傳遞而來，用於退款計算
	LockedCurrency string            `json:"locked_currency"` // 鎖定幣種
	RemainingQty   decimal.Decimal   `json:"remaining_qty"`   // 撮合後剩餘數量（用於判斷 PartialFilled / STP）
	Trades         []*matching.Trade `json:"trades"`          // 本次撮合產生的所有成交記錄
}

// TradeExecutedEvent 每筆個別成交事件（供 WebSocket 推播與外部訂閱）
type TradeExecutedEvent struct {
	EventType    EventType       `json:"event_type"`
	Symbol       string          `json:"symbol"`
	TradeID      uuid.UUID       `json:"trade_id"`
	MakerOrderID uuid.UUID       `json:"maker_order_id"`
	TakerOrderID uuid.UUID       `json:"taker_order_id"`
	Price        decimal.Decimal `json:"price"`
	Quantity     decimal.Decimal `json:"quantity"`
	CreatedAt    int64           `json:"created_at"` // Unix 毫秒
}

// OrderBookUpdatedEvent 掛單簿快照更新事件（撮合完成或撤單後觸發）
type OrderBookUpdatedEvent struct {
	EventType EventType                   `json:"event_type"`
	Symbol    string                      `json:"symbol"`
	Snapshot  *matching.OrderBookSnapshot `json:"snapshot"`
}
