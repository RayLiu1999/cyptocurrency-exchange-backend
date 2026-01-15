package matching

import "sort"

// OrderBook 訂單簿，儲存買賣訂單
type OrderBook struct {
	symbol string
	bids   []*Order // 買單列表 (按價格降序排列)
	asks   []*Order // 賣單列表 (按價格升序排列)
}

// NewOrderBook 建立新的訂單簿
func NewOrderBook(symbol string) *OrderBook {
	return &OrderBook{
		symbol: symbol,
		bids:   make([]*Order, 0),
		asks:   make([]*Order, 0),
	}
}

// Symbol 返回交易對
func (ob *OrderBook) Symbol() string {
	return ob.symbol
}

// BidCount 返回買單數量
func (ob *OrderBook) BidCount() int {
	return len(ob.bids)
}

// AskCount 返回賣單數量
func (ob *OrderBook) AskCount() int {
	return len(ob.asks)
}

// AddOrder 新增訂單到訂單簿
func (ob *OrderBook) AddOrder(order *Order) {
	if order.Side == SideBuy {
		ob.bids = append(ob.bids, order)
		// 按價格降序排列 (價格高的在前面)
		sort.Slice(ob.bids, func(i, j int) bool {
			return ob.bids[i].Price.GreaterThan(ob.bids[j].Price)
		})
	} else {
		ob.asks = append(ob.asks, order)
		// 按價格升序排列 (價格低的在前面)
		sort.Slice(ob.asks, func(i, j int) bool {
			return ob.asks[i].Price.LessThan(ob.asks[j].Price)
		})
	}
}

// BestBid 返回最佳買單 (價格最高)
func (ob *OrderBook) BestBid() *Order {
	if len(ob.bids) == 0 {
		return nil
	}
	return ob.bids[0]
}

// BestAsk 返回最佳賣單 (價格最低)
func (ob *OrderBook) BestAsk() *Order {
	if len(ob.asks) == 0 {
		return nil
	}
	return ob.asks[0]
}

// RemoveBestBid 移除最佳買單
func (ob *OrderBook) RemoveBestBid() {
	if len(ob.bids) > 0 {
		ob.bids = ob.bids[1:]
	}
}

// RemoveBestAsk 移除最佳賣單
func (ob *OrderBook) RemoveBestAsk() {
	if len(ob.asks) > 0 {
		ob.asks = ob.asks[1:]
	}
}
