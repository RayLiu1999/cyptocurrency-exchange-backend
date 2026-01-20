package matching

import (
	"sync"

	"github.com/google/uuid"
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
