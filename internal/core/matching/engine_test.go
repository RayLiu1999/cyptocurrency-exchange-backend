package matching

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

/*
=== TDD TODO List: Matching Engine ===

Phase 1: 基本結構 (Foundation) ✅ DONE
- [x] 1.1 建立 OrderBook 結構，能儲存買賣訂單
- [x] 1.2 新增訂單到 OrderBook (買單/賣單分開儲存)
- [x] 1.3 空 OrderBook 收到訂單，不應產生成交

Phase 2: 基本成交 (Basic Matching) ✅ DONE
- [x] 2.1 買單價格 >= 最低賣單價格時，應產生成交
- [x] 2.2 成交價格 = Maker 價格
- [x] 2.3 成交後，訂單從 OrderBook 移除

Phase 3: 價格優先 (Price Priority) ✅ DONE
- [x] 3.1 賣方：價格低的優先成交
- [x] 3.2 買方：價格高的優先成交

Phase 4: 時間優先 (Time Priority - FIFO) ✅ DONE
- [x] 4.1 同價位時，先進場的訂單優先成交

Phase 5: 部分成交 (Partial Fill) ✅ DONE
- [x] 5.1 Taker 數量 > Maker 時，連續成交多個 Maker
- [x] 5.2 Taker 數量 < Maker 時，Maker 部分成交
- [x] 5.3 剩餘數量留在 OrderBook

Phase 6: 連續成交 (Multiple Matches) ✅ DONE
- [x] 6.1 一個大單與多個對手方連續成交

Phase 1.5a: 市價單 (Market Order) ✅ DONE
- [x] 市價買單吃掉最低價賣單
- [x] 市價賣單吃掉最高價買單
- [x] 市價單連續成交多個 Maker
- [x] 市價單數量 > 訂單簿深度時，部分成交

Phase 1.5b: 多交易對 (Multi-Symbol) ✅ DONE
- [x] 不同交易對的訂單不會互相撮合
- [x] 同交易對可正常撮合
- [x] GetEngine 重複呼叫應返回同一個 Engine

Phase 7: 邊界條件 (Edge Cases) - 暫緩
- [ ] 7.1 價格不匹配時，不成交
- [ ] 7.2 自成交防護

=====================================
*/

// ============================================================
// Phase 1: 基本結構 ✅
// ============================================================

func TestOrderBook_NewOrderBook_CreatesEmptyBook(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	assert.NotNil(t, book)
	assert.Equal(t, "BTC-USD", book.Symbol())
	assert.Equal(t, 0, book.BidCount())
	assert.Equal(t, 0, book.AskCount())
}

func TestOrderBook_AddOrder_BuyOrderGoesToBids(t *testing.T) {
	book := NewOrderBook("BTC-USD")
	order := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	book.AddOrder(order)

	assert.Equal(t, 1, book.BidCount())
	assert.Equal(t, 0, book.AskCount())
}

func TestOrderBook_AddOrder_SellOrderGoesToAsks(t *testing.T) {
	book := NewOrderBook("BTC-USD")
	order := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))

	book.AddOrder(order)

	assert.Equal(t, 0, book.BidCount())
	assert.Equal(t, 1, book.AskCount())
}

func TestEngine_EmptyBook_NoTrades(t *testing.T) {
	engine := NewEngine("BTC-USD")
	order := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	trades := engine.Process(order)

	assert.Empty(t, trades, "空 OrderBook 不應產生成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "訂單應進入買方佇列")
}

// ============================================================
// Phase 2: 基本成交 ✅
// ============================================================

func TestEngine_BuyPriceMatchesSellPrice_TradeExecuted(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 先掛一個賣單 (Maker): 價格 100，數量 1
	sellOrder := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// 再來一個買單 (Taker): 價格 100，數量 1
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity, "成交數量應為 1")
}

func TestEngine_TradePrice_EqualsMakerPrice(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// Maker 賣單: 價格 100
	sellOrder := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// Taker 買單: 價格 105 (願意出更高價)
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(105), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "成交價格應為 Maker 價格 100")
}

func TestEngine_AfterFullMatch_OrdersRemovedFromBook(t *testing.T) {
	engine := NewEngine("BTC-USD")

	sellOrder := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sellOrder)
	assert.Equal(t, 1, engine.OrderBook().AskCount())

	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(buyOrder)

	assert.Equal(t, 0, engine.OrderBook().AskCount(), "成交後賣單應移除")
	assert.Equal(t, 0, engine.OrderBook().BidCount(), "完全成交的買單不應進入訂單簿")
}

// ============================================================
// Phase 3: 價格優先
// ============================================================

// TODO 3.1: 賣方價格低的優先成交
func TestEngine_PricePriority_LowestAskMatchesFirst(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 先掛兩個賣單：價格 102 和 100
	sell1 := NewOrder(SideSell, decimal.NewFromInt(102), decimal.NewFromInt(1))
	sell2 := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sell1)
	engine.Process(sell2)

	// 買單：價格 105，數量 1 (只買一個)
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(105), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應優先與價格最低的賣單 (100) 成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "價格 102 的賣單應還在")
}

// TODO 3.2: 買方價格高的優先成交
func TestEngine_PricePriority_HighestBidMatchesFirst(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 先掛兩個買單：價格 98 和 100
	buy1 := NewOrder(SideBuy, decimal.NewFromInt(98), decimal.NewFromInt(1))
	buy2 := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(buy1)
	engine.Process(buy2)

	// 賣單：價格 95，數量 1 (只賣一個)
	sellOrder := NewOrder(SideSell, decimal.NewFromInt(95), decimal.NewFromInt(1))

	trades := engine.Process(sellOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應優先與價格最高的買單 (100) 成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "價格 98 的買單應還在")
}

// ============================================================
// Phase 4: 時間優先 (FIFO) ✅
// ============================================================

// TODO 4.1: 同價位時，先進場的訂單優先成交
func TestEngine_TimePriority_FirstOrderMatchesFirst(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個同價位的賣單
	sell1 := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	sell2 := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sell1)
	engine.Process(sell2)

	// 買單：只買一個
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, sell1.ID, trades[0].MakerOrderID, "應優先與先進場的賣單成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "第二個賣單應還在")
}

// ============================================================
// Phase 5: 部分成交
// ============================================================

// TODO 5.1: Taker 數量 > Maker 時，連續成交多個 Maker
func TestEngine_PartialFill_TakerLargerThanMaker(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個小賣單，每個數量 1
	sell1 := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	sell2 := NewOrder(SideSell, decimal.NewFromInt(101), decimal.NewFromInt(1))
	engine.Process(sell1)
	engine.Process(sell2)

	// 大買單：數量 2，會吃掉兩個賣單
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(105), decimal.NewFromInt(2))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 2, "應產生兩筆成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity)
	assert.Equal(t, decimal.NewFromInt(1), trades[1].Quantity)
	assert.Equal(t, 0, engine.OrderBook().AskCount(), "所有賣單應被成交")
	assert.Equal(t, 0, engine.OrderBook().BidCount(), "買單完全成交，不應進入訂單簿")
}

// TODO 5.2: Taker 數量 < Maker 時，Maker 部分成交
func TestEngine_PartialFill_TakerSmallerThanMaker(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛一個大賣單：數量 10
	sellOrder := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(10))
	engine.Process(sellOrder)

	// 小買單：數量 3
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(3))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(3), trades[0].Quantity, "成交數量應為 3")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "賣單應還在訂單簿")

	// 驗證剩餘數量
	remainingSell := engine.OrderBook().BestAsk()
	assert.Equal(t, decimal.NewFromInt(7), remainingSell.Quantity, "賣單剩餘數量應為 7")
}

// TODO 5.3: 剩餘數量留在 OrderBook
func TestEngine_PartialFill_TakerRemainsInBook(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛一個小賣單：數量 2
	sellOrder := NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(2))
	engine.Process(sellOrder)

	// 大買單：數量 5，只能成交 2
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(5))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(2), trades[0].Quantity)
	assert.Equal(t, 0, engine.OrderBook().AskCount(), "賣單完全成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "買單剩餘部分應進入訂單簿")

	// 驗證剩餘數量
	remainingBuy := engine.OrderBook().BestBid()
	assert.Equal(t, decimal.NewFromInt(3), remainingBuy.Quantity, "買單剩餘數量應為 3")
}

// ============================================================
// Phase 6: 連續成交
// ============================================================

// TODO 6.1: 一個大單與多個對手方連續成交
func TestEngine_MultipleMatches_LargeOrderMatchesMultiple(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛三個賣單，不同價格
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1)))
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(101), decimal.NewFromInt(2)))
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(102), decimal.NewFromInt(3)))

	// 大買單：數量 5，價格 102
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(102), decimal.NewFromInt(5))

	trades := engine.Process(buyOrder)

	// 應該成交 1 + 2 + 2 = 5 (第三個賣單部分成交)
	assert.Len(t, trades, 3, "應與三個賣單成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity, "第一筆成交 1")
	assert.Equal(t, decimal.NewFromInt(2), trades[1].Quantity, "第二筆成交 2")
	assert.Equal(t, decimal.NewFromInt(2), trades[2].Quantity, "第三筆成交 2 (部分)")

	assert.Equal(t, 1, engine.OrderBook().AskCount(), "應剩一個賣單")
	remainingSell := engine.OrderBook().BestAsk()
	assert.Equal(t, decimal.NewFromInt(1), remainingSell.Quantity, "剩餘賣單數量為 1")
}

// ============================================================
// Phase 1.5: 市價單 (Market Order)
// ============================================================

// TODO 1.5.1: 市價買單吃掉最低價賣單
func TestEngine_MarketBuyOrder_MatchesLowestAsk(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個賣單
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1)))
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(102), decimal.NewFromInt(1)))

	// 市價買單：數量 1
	marketBuy := NewMarketOrder(SideBuy, decimal.NewFromInt(1))

	trades := engine.Process(marketBuy)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應以最低賣價 100 成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "價格 102 的賣單應還在")
}

// TODO 1.5.2: 市價賣單吃掉最高價買單
func TestEngine_MarketSellOrder_MatchesHighestBid(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個買單
	engine.Process(NewOrder(SideBuy, decimal.NewFromInt(98), decimal.NewFromInt(1)))
	engine.Process(NewOrder(SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1)))

	// 市價賣單：數量 1
	marketSell := NewMarketOrder(SideSell, decimal.NewFromInt(1))

	trades := engine.Process(marketSell)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應以最高買價 100 成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "價格 98 的買單應還在")
}

// TODO 1.5.3: 市價單連續成交多個 Maker
func TestEngine_MarketOrder_MatchesMultipleMakers(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛三個賣單
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1)))
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(101), decimal.NewFromInt(2)))
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(102), decimal.NewFromInt(3)))

	// 市價買單：數量 4 (吃掉前兩個賣單 + 第三個部分)
	marketBuy := NewMarketOrder(SideBuy, decimal.NewFromInt(4))

	trades := engine.Process(marketBuy)

	assert.Len(t, trades, 3, "應與三個賣單成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity, "第一筆成交 1")
	assert.Equal(t, decimal.NewFromInt(2), trades[1].Quantity, "第二筆成交 2")
	assert.Equal(t, decimal.NewFromInt(1), trades[2].Quantity, "第三筆成交 1 (部分)")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "應剩一個賣單")
}

// TODO 1.5.4: 市價單數量 > 訂單簿深度時，部分成交
func TestEngine_MarketOrder_PartialFillWhenInsufficientLiquidity(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛一個賣單：數量 2
	engine.Process(NewOrder(SideSell, decimal.NewFromInt(100), decimal.NewFromInt(2)))

	// 市價買單：數量 5 (超過訂單簿深度)
	marketBuy := NewMarketOrder(SideBuy, decimal.NewFromInt(5))

	trades := engine.Process(marketBuy)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(2), trades[0].Quantity, "只能成交 2")
	assert.Equal(t, 0, engine.OrderBook().AskCount(), "賣單應被吃光")
	// 市價單剩餘部分不進入訂單簿
	assert.Equal(t, 0, engine.OrderBook().BidCount(), "市價單剩餘不應進入訂單簿")
}

// ============================================================
// Phase 1.5: 多交易對支援 (Multi-Symbol)
// ============================================================

// TODO 3.1: 不同交易對的訂單不會互相撮合
func TestEngineManager_DifferentSymbols_NoMatch(t *testing.T) {
	manager := NewEngineManager()

	// BTC-USD 掛一個賣單
	btcEngine := manager.GetEngine("BTC-USD")
	btcSell := NewOrder(SideSell, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	btcEngine.Process(btcSell)

	// ETH-USD 掛一個買單 (不應與 BTC-USD 的賣單撮合)
	ethEngine := manager.GetEngine("ETH-USD")
	ethBuy := NewOrder(SideBuy, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	trades := ethEngine.Process(ethBuy)

	assert.Empty(t, trades, "不同交易對不應撮合")
	assert.Equal(t, 1, btcEngine.OrderBook().AskCount(), "BTC 賣單應還在")
	assert.Equal(t, 1, ethEngine.OrderBook().BidCount(), "ETH 買單應進入訂單簿")
}

// TODO 3.2: 同交易對可正常撮合
func TestEngineManager_SameSymbol_MatchesCorrectly(t *testing.T) {
	manager := NewEngineManager()

	engine := manager.GetEngine("BTC-USD")

	// 掛賣單
	sellOrder := NewOrder(SideSell, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// 同交易對買單應撮合
	buyOrder := NewOrder(SideBuy, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1, "同交易對應撮合")
	assert.Equal(t, 0, engine.OrderBook().AskCount())
	assert.Equal(t, 0, engine.OrderBook().BidCount())
}

// TODO 3.3: GetEngine 重複呼叫應返回同一個 Engine
func TestEngineManager_GetEngine_ReturnsSameInstance(t *testing.T) {
	manager := NewEngineManager()

	engine1 := manager.GetEngine("BTC-USD")
	engine2 := manager.GetEngine("BTC-USD")

	assert.Same(t, engine1, engine2, "應返回同一個 Engine 實例")
}
