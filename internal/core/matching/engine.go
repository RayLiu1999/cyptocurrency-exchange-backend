package matching

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Engine 撮合引擎
type Engine struct {
	orderBook *OrderBook
	mu        sync.Mutex
}

// NewEngine 建立新的撮合引擎
func NewEngine(symbol string) *Engine {
	return &Engine{
		orderBook: NewOrderBook(symbol),
	}
}

// OrderBook 返回訂單簿
func (e *Engine) OrderBook() *OrderBook {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.orderBook
}

// Process 處理新訂單，返回成交結果
func (e *Engine) Process(order *Order) []*Trade {
	e.mu.Lock()
	defer e.mu.Unlock()

	var trades []*Trade

	if order.Side == SideBuy {
		// 買單：嘗試與賣單匹配
		trades = e.matchBuyOrder(order)
	} else {
		// 賣單：嘗試與買單匹配
		trades = e.matchSellOrder(order)
	}

	// 如果還有剩餘數量，限價單加入訂單簿，市價單不加入
	if order.Quantity.IsPositive() && order.Type != TypeMarket {
		e.orderBook.AddOrder(order)
	}

	return trades
}

// matchBuyOrder 買單撮合邏輯
func (e *Engine) matchBuyOrder(buyOrder *Order) []*Trade {
	var trades []*Trade

	for {
		// 取得最佳賣單 (價格最低)
		bestAsk := e.orderBook.BestAsk()
		if bestAsk == nil {
			break // 沒有賣單可匹配
		}

		// Wash Trade Prevention: 避免左手換右手
		if buyOrder.UserID == bestAsk.UserID {
			// 如果對手是自己，不予撮合，直接從 OrderBook 移除自己的舊單來釋放流動性，然後看下一個最佳賣單
			e.orderBook.RemoveBestAsk()
			continue
		}

		// 檢查價格是否匹配：限價單需檢查，市價單直接成交
		if buyOrder.Type != TypeMarket && buyOrder.Price.LessThan(bestAsk.Price) {
			break // 價格不匹配
		}

		// 計算成交數量
		matchQty := buyOrder.Quantity
		if bestAsk.Quantity.LessThan(matchQty) {
			matchQty = bestAsk.Quantity
		}

		// 建立成交記錄
		trade := &Trade{
			ID:           uuid.New(),
			Symbol:       e.orderBook.Symbol(),
			MakerOrderID: bestAsk.ID,
			TakerOrderID: buyOrder.ID,
			Price:        bestAsk.Price, // 成交價格 = Maker 價格
			Quantity:     matchQty,
			CreatedAt:    time.Now().UTC(),
		}
		trades = append(trades, trade)

		// 更新訂單數量
		buyOrder.Quantity = buyOrder.Quantity.Sub(matchQty)
		bestAsk.Quantity = bestAsk.Quantity.Sub(matchQty)

		// 如果賣單完全成交，從訂單簿移除
		if bestAsk.Quantity.IsZero() {
			e.orderBook.RemoveBestAsk()
		}

		// 如果買單完全成交，退出
		if buyOrder.Quantity.IsZero() {
			break
		}
	}

	return trades
}

// matchSellOrder 賣單撮合邏輯
func (e *Engine) matchSellOrder(sellOrder *Order) []*Trade {
	var trades []*Trade

	for {
		// 取得最佳買單 (價格最高)
		bestBid := e.orderBook.BestBid()
		if bestBid == nil {
			break // 沒有買單可匹配
		}

		// Wash Trade Prevention: 避免左手換右手
		if sellOrder.UserID == bestBid.UserID {
			e.orderBook.RemoveBestBid()
			continue
		}

		// 檢查價格是否匹配：限價單需檢查，市價單直接成交
		if sellOrder.Type != TypeMarket && sellOrder.Price.GreaterThan(bestBid.Price) {
			break // 價格不匹配
		}

		// 計算成交數量
		matchQty := sellOrder.Quantity
		if bestBid.Quantity.LessThan(matchQty) {
			matchQty = bestBid.Quantity
		}

		// 建立成交記錄
		trade := &Trade{
			ID:           uuid.New(),
			Symbol:       e.orderBook.Symbol(),
			MakerOrderID: bestBid.ID,
			TakerOrderID: sellOrder.ID,
			Price:        bestBid.Price, // 成交價格 = Maker 價格
			Quantity:     matchQty,
			CreatedAt:    time.Now().UTC(),
		}
		trades = append(trades, trade)

		// 更新訂單數量
		sellOrder.Quantity = sellOrder.Quantity.Sub(matchQty)
		bestBid.Quantity = bestBid.Quantity.Sub(matchQty)

		// 如果買單完全成交，從訂單簿移除
		if bestBid.Quantity.IsZero() {
			e.orderBook.RemoveBestBid()
		}

		// 如果賣單完全成交，退出
		if sellOrder.Quantity.IsZero() {
			break
		}
	}

	return trades
}

// Cancel 處理取消訂單，從訂單簿移除
func (e *Engine) Cancel(orderID uuid.UUID, side OrderSide) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.orderBook.RemoveOrder(orderID, side)
}

// GetOrderBookSnapshot 取得訂單簿快照 (Thread-Safe)
// depth: 每個方向返回的深度層級數量 (0 表示返回全部)
func (e *Engine) GetOrderBookSnapshot(depth int) *OrderBookSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	snapshot := &OrderBookSnapshot{
		Symbol: e.orderBook.Symbol(),
		Bids:   make([]OrderBookLevel, 0),
		Asks:   make([]OrderBookLevel, 0),
	}

	// 處理買單 (Bids)
	for i, order := range e.orderBook.bids {
		if depth > 0 && i >= depth {
			break
		}
		snapshot.Bids = append(snapshot.Bids, OrderBookLevel{
			Price:    order.Price,
			Quantity: order.Quantity,
		})
	}

	// 處理賣單 (Asks)
	for i, order := range e.orderBook.asks {
		if depth > 0 && i >= depth {
			break
		}
		snapshot.Asks = append(snapshot.Asks, OrderBookLevel{
			Price:    order.Price,
			Quantity: order.Quantity,
		})
	}

	return snapshot
}

// EstimateMarketBuyRequiredFunds 預估市價買單所需資金
func (e *Engine) EstimateMarketBuyRequiredFunds(quantity decimal.Decimal) (decimal.Decimal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.orderBook.EstimateMarketBuyRequiredFunds(quantity)
}
