package engine

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

// ============================================================
// Phase 1: 基本結構 ✅
// ============================================================

// TestOrderBook_NewOrderBook_CreatesEmptyBook 測試建立空的訂單簿
func TestOrderBook_NewOrderBook_CreatesEmptyBook(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	assert.NotNil(t, book)
	assert.Equal(t, "BTC-USD", book.Symbol())
	assert.Equal(t, 0, book.BidCount())
	assert.Equal(t, 0, book.AskCount())
}

// TestOrderBook_AddOrder_BuyOrderGoesToBids 測試買單加入訂單簿
func TestOrderBook_AddOrder_BuyOrderGoesToBids(t *testing.T) {
	book := NewOrderBook("BTC-USD")
	order := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	book.AddOrder(order)

	assert.Equal(t, 1, book.BidCount())
	assert.Equal(t, 0, book.AskCount())
}

// TestOrderBook_AddOrder_SellOrderGoesToAsks 測試賣單加入訂單簿
func TestOrderBook_AddOrder_SellOrderGoesToAsks(t *testing.T) {
	book := NewOrderBook("BTC-USD")
	order := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))

	book.AddOrder(order)

	assert.Equal(t, 0, book.BidCount())
	assert.Equal(t, 1, book.AskCount())
}

// TestEngine_EmptyBook_NoTrades 測試空訂單簿不產生成交
func TestEngine_EmptyBook_NoTrades(t *testing.T) {
	engine := NewEngine("BTC-USD")
	order := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	trades := engine.Process(order)

	assert.Empty(t, trades, "空 OrderBook 不應產生成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "訂單應進入買方佇列")
}

// ============================================================
// Phase 2: 基本成交 ✅
// ============================================================

// TestEngine_BuyPriceMatchesSellPrice_TradeExecuted 測試買單價格匹配賣單價格時產生成交
func TestEngine_BuyPriceMatchesSellPrice_TradeExecuted(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 先掛一個賣單 (Maker): 價格 100，數量 1
	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// 再來一個買單 (Taker): 價格 100，數量 1
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity, "成交數量應為 1")
}

// TestEngine_TradePrice_EqualsMakerPrice 測試成交價格等於 Maker 價格
func TestEngine_TradePrice_EqualsMakerPrice(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// Maker 賣單: 價格 100
	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// Taker 買單: 價格 105 (願意出更高價)
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(105), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "成交價格應為 Maker 價格 100")
}

// TestEngine_AfterFullMatch_OrdersRemovedFromBook 測試完全成交後訂單從訂單簿移除
func TestEngine_AfterFullMatch_OrdersRemovedFromBook(t *testing.T) {
	engine := NewEngine("BTC-USD")

	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sellOrder)
	assert.Equal(t, 1, engine.OrderBook().AskCount())

	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(buyOrder)

	assert.Equal(t, 0, engine.OrderBook().AskCount(), "成交後賣單應移除")
	assert.Equal(t, 0, engine.OrderBook().BidCount(), "完全成交的買單不應進入訂單簿")
}

// ============================================================
// Phase 3: 價格優先
// ============================================================

func TestEngine_PricePriority_LowestAskMatchesFirst(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 先掛兩個賣單：價格 102 和 100
	sell1 := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(102), decimal.NewFromInt(1))
	sell2 := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sell1)
	engine.Process(sell2)

	// 買單：價格 105，數量 1 (只買一個)
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(105), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應優先與價格最低的賣單 (100) 成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "價格 102 的賣單應還在")
}

// TestEngine_PricePriority_HighestBidMatchesFirst 測試買單價格高的優先成交
func TestEngine_PricePriority_HighestBidMatchesFirst(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 先掛兩個買單：價格 98 和 100
	buy1 := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(98), decimal.NewFromInt(1))
	buy2 := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(buy1)
	engine.Process(buy2)

	// 賣單：價格 95，數量 1 (只賣一個)
	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(95), decimal.NewFromInt(1))

	trades := engine.Process(sellOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應優先與價格最高的買單 (100) 成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "價格 98 的買單應還在")
}

// ============================================================
// Phase 4: 時間優先 (FIFO) ✅
// ============================================================

// TestEngine_TimePriority_FirstOrderMatchesFirst 測試同價位時，先進場的訂單優先成交
func TestEngine_TimePriority_FirstOrderMatchesFirst(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個同價位的賣單
	sell1 := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	sell2 := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	engine.Process(sell1)
	engine.Process(sell2)

	// 買單：只買一個
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1)
	assert.Equal(t, sell1.ID, trades[0].MakerOrderID, "應優先與先進場的賣單成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "第二個賣單應還在")
}

// ============================================================
// Phase 5: 部分成交
// ============================================================

// TestEngine_PartialFill_TakerLargerThanMaker 測試 Taker 數量 > Maker 時，連續成交多個 Maker
func TestEngine_PartialFill_TakerLargerThanMaker(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個小賣單，每個數量 1
	sell1 := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1))
	sell2 := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(101), decimal.NewFromInt(1))
	engine.Process(sell1)
	engine.Process(sell2)

	// 大買單：數量 2，會吃掉兩個賣單
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(105), decimal.NewFromInt(2))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 2, "應產生兩筆成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity)
	assert.Equal(t, decimal.NewFromInt(1), trades[1].Quantity)
	assert.Equal(t, 0, engine.OrderBook().AskCount(), "所有賣單應被成交")
	assert.Equal(t, 0, engine.OrderBook().BidCount(), "買單完全成交，不應進入訂單簿")
}

// TestEngine_PartialFill_TakerSmallerThanMaker 測試 Taker 數量 < Maker 時，Maker 部分成交
func TestEngine_PartialFill_TakerSmallerThanMaker(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛一個大賣單：數量 10
	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(10))
	engine.Process(sellOrder)

	// 小買單：數量 3
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(3))

	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(3), trades[0].Quantity, "成交數量應為 3")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "賣單應還在訂單簿")

	// 驗證剩餘數量
	remainingSell := engine.OrderBook().BestAsk()
	assert.Equal(t, decimal.NewFromInt(7), remainingSell.Quantity, "賣單剩餘數量應為 7")
}

// TestEngine_PartialFill_TakerRemainsInBook 測試 Taker 數量 < Maker 時，Maker 部分成交
func TestEngine_PartialFill_TakerRemainsInBook(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛一個小賣單：數量 2
	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(2))
	engine.Process(sellOrder)

	// 大買單：數量 5，只能成交 2
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(5))

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

// TestEngine_MultipleMatches_LargeOrderMatchesMultiple 測試一個大單與多個對手方連續成交
func TestEngine_MultipleMatches_LargeOrderMatchesMultiple(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛三個賣單，不同價格
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1)))
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(101), decimal.NewFromInt(2)))
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(102), decimal.NewFromInt(3)))

	// 大買單：數量 5，價格 102
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(102), decimal.NewFromInt(5))

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

// TestEngine_MarketBuyOrder_MatchesLowestAsk 測試市價買單吃掉最低價賣單
func TestEngine_MarketBuyOrder_MatchesLowestAsk(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個賣單
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1)))
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(102), decimal.NewFromInt(1)))

	// 市價買單：數量 1
	marketBuy := NewMarketOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(1))

	trades := engine.Process(marketBuy)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應以最低賣價 100 成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "價格 102 的賣單應還在")
}

// TestEngine_MarketSellOrder_MatchesHighestBid 測試市價賣單吃掉最高價買單
func TestEngine_MarketSellOrder_MatchesHighestBid(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛兩個買單
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(98), decimal.NewFromInt(1)))
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(100), decimal.NewFromInt(1)))

	// 市價賣單：數量 1
	marketSell := NewMarketOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(1))

	trades := engine.Process(marketSell)

	assert.Len(t, trades, 1, "應產生一筆成交")
	assert.Equal(t, decimal.NewFromInt(100), trades[0].Price, "應以最高買價 100 成交")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "價格 98 的買單應還在")
}

// TestEngine_MarketOrder_MatchesMultipleMakers 測試市價單連續成交多個 Maker
func TestEngine_MarketOrder_MatchesMultipleMakers(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛三個賣單
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(1)))
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(101), decimal.NewFromInt(2)))
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(102), decimal.NewFromInt(3)))

	// 市價買單：數量 4 (吃掉前兩個賣單 + 第三個部分)
	marketBuy := NewMarketOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(4))

	trades := engine.Process(marketBuy)

	assert.Len(t, trades, 3, "應與三個賣單成交")
	assert.Equal(t, decimal.NewFromInt(1), trades[0].Quantity, "第一筆成交 1")
	assert.Equal(t, decimal.NewFromInt(2), trades[1].Quantity, "第二筆成交 2")
	assert.Equal(t, decimal.NewFromInt(1), trades[2].Quantity, "第三筆成交 1 (部分)")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "應剩一個賣單")
}

// TestEngine_MarketOrder_PartialFillWhenInsufficientLiquidity 測試市價單數量 > 訂單簿深度時，部分成交
func TestEngine_MarketOrder_PartialFillWhenInsufficientLiquidity(t *testing.T) {
	engine := NewEngine("BTC-USD")

	// 掛一個賣單：數量 2
	engine.Process(NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(100), decimal.NewFromInt(2)))

	// 市價買單：數量 5 (超過訂單簿深度)
	marketBuy := NewMarketOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(5))

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

// TestEngineManager_DifferentSymbols_NoMatch 測試不同交易對的訂單不會互相撮合
func TestEngineManager_DifferentSymbols_NoMatch(t *testing.T) {
	manager := NewEngineManager()

	// BTC-USD 掛一個賣單
	btcEngine := manager.GetEngine("BTC-USD")
	btcSell := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	btcEngine.Process(btcSell)

	// ETH-USD 掛一個買單 (不應與 BTC-USD 的賣單撮合)
	ethEngine := manager.GetEngine("ETH-USD")
	ethBuy := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	trades := ethEngine.Process(ethBuy)

	assert.Empty(t, trades, "不同交易對不應撮合")
	assert.Equal(t, 1, btcEngine.OrderBook().AskCount(), "BTC 賣單應還在")
	assert.Equal(t, 1, ethEngine.OrderBook().BidCount(), "ETH 買單應進入訂單簿")
}

// TestEngineManager_SameSymbol_MatchesCorrectly 測試同交易對可正常撮合
func TestEngineManager_SameSymbol_MatchesCorrectly(t *testing.T) {
	manager := NewEngineManager()

	engine := manager.GetEngine("BTC-USD")

	// 掛賣單
	sellOrder := NewOrder(uuid.New(), uuid.New(), SideSell, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// 同交易對買單應撮合
	buyOrder := NewOrder(uuid.New(), uuid.New(), SideBuy, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	trades := engine.Process(buyOrder)

	assert.Len(t, trades, 1, "同交易對應撮合")
	assert.Equal(t, 0, engine.OrderBook().AskCount())
	assert.Equal(t, 0, engine.OrderBook().BidCount())
}

// TestEngineManager_GetEngine_ReturnsSameInstance 測試 GetEngine 重複呼叫應返回同一個 Engine
func TestEngineManager_GetEngine_ReturnsSameInstance(t *testing.T) {
	manager := NewEngineManager()

	engine1 := manager.GetEngine("BTC-USD")
	engine2 := manager.GetEngine("BTC-USD")

	assert.Same(t, engine1, engine2, "應返回同一個 Engine 實例")
}

// ============================================================
// Phase 7: 邊界條件 (Edge Cases)
// ============================================================

// TestEngine_PriceMismatch_NoTradeExecuted 測試買價低於賣價時，不得成交，雙方訂單應保留在訂單簿
func TestEngine_PriceMismatch_NoTradeExecuted(t *testing.T) {
	// Arrange
	engine := NewEngine("BTC-USD")
	sellID := uuid.New()
	buyID := uuid.New()

	// 掛一筆賣單，賣價 51000
	sellOrder := NewOrder(sellID, uuid.New(), SideSell, decimal.NewFromInt(51000), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// 買單價格 50000 < 賣單 51000，不應成交
	buyOrder := NewOrder(buyID, uuid.New(), SideBuy, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	trades := engine.Process(buyOrder)

	// Assert
	assert.Empty(t, trades, "價格不匹配時不得產生成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "賣單應保留在訂單簿")
	assert.Equal(t, 1, engine.OrderBook().BidCount(), "買單應保留在訂單簿")
}

// TestEngine_SelfTrade_Prevented 測試同一個 user_id 的買賣單不可彼此成交（自成交防護）
func TestEngine_SelfTrade_Prevented(t *testing.T) {
	// Arrange
	engine := NewEngine("BTC-USD")
	sameUserID := uuid.New()

	// 同一個用戶先掛賣單
	sellOrder := NewOrder(uuid.New(), sameUserID, SideSell, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	engine.Process(sellOrder)

	// 同一個用戶再送買單，價格匹配但應拒絕撮合 (觸發 STP - Cancel Newest)
	buyOrder := NewOrder(uuid.New(), sameUserID, SideBuy, decimal.NewFromInt(50000), decimal.NewFromInt(1))
	trades := engine.Process(buyOrder)

	// Assert
	assert.Empty(t, trades, "自成交防護：不得對自己的訂單成交")
	assert.Equal(t, 1, engine.OrderBook().AskCount(), "賣單應保留在訂單簿")

	// 因為觸發了 Cancel Newest 的 STP 機制，買單的 Quantity 被設為 0，所以不應進入訂單簿
	assert.Equal(t, 0, engine.OrderBook().BidCount(), "被拒絕的買單因 STP 被取消，不進入訂單簿")
}

// ============================================================
// 效能壓力測試 (Benchmark)
// 對標 AXS 專案的 Processor Throughput
// ============================================================

// BenchmarkEngineMatch 測試引擎的撮合效能
func BenchmarkEngineMatch(b *testing.B) {
	engine := NewEngine("BTC-USD")
	makerID := uuid.New()
	takerID := uuid.New()

	// 假設價格與數量固定，直接在迴圈外分配好記憶體 (不計入時間)
	price := decimal.NewFromInt(50000)
	qty := decimal.NewFromInt(1)

	bigSell := NewOrder(uuid.New(), makerID, SideSell, price, decimal.NewFromInt(int64(b.N+1000)))
	engine.Process(bigSell)
	// ====== 從這條線以下才開始真正計時測量 ======
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 傳入 uuid.UUID{} (空 UUID) 來避開 Syscall 隨機數產生的巨大延遲
		buyOrder := NewOrder(uuid.UUID{}, takerID, SideBuy, price, qty)
		engine.Process(buyOrder)
	}
}
