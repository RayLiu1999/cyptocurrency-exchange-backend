package core

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/outbox"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ExchangeServiceImpl 交易所服務實作
type ExchangeServiceImpl struct {
	orderRepo     OrderRepository
	accountRepo   AccountRepository
	tradeRepo     TradeRepository
	userRepo      UserRepository
	tradeListener TradeEventListener
	txManager     DBTransaction
	engineManager *matching.EngineManager
	cacheRepo     CacheRepository  // Redis 快取 (可選)
	eventBus      EventPublisher   // Kafka 事件發布 (可選，無 Kafka 時為 nil)
	outboxRepo    *outbox.Repository // Outbox Pattern 的資料庫操作 (可選)
}

// AccountUpdate 資金變更紀錄
type AccountUpdate struct {
	UserID   uuid.UUID
	Currency string
	Amount   decimal.Decimal
	Unlock   decimal.Decimal
}

// AggregateAndSortAccountUpdates 聚合並排序資金變更
// 定義函式：接收一組原始的 AccountUpdate 陣列，回傳處理過（合併且排序）的新陣列
func AggregateAndSortAccountUpdates(updates []AccountUpdate) []AccountUpdate {

	// --- 第一階段：聚合 (Aggregation) ---

	// 建立一個 Map，用「使用者ID+幣種」當作 Key，目的是合併同一個人在同一個幣種的多筆變動
	aggMap := make(map[string]*AccountUpdate)

	for _, up := range updates {
		// 產生唯一的 Key，例如："user1_BTC"
		key := up.UserID.String() + "_" + up.Currency

		if existing, ok := aggMap[key]; ok {
			// 如果這個 Key 已經在 Map 裡了，就把數值疊加進去
			// 使用 decimal.Add 確保金額計算精準，不丟失精度
			existing.Amount = existing.Amount.Add(up.Amount) // 餘額變動
			existing.Unlock = existing.Unlock.Add(up.Unlock) // 解鎖金額
		} else {
			// 如果是第一次遇到這個人跟幣種，就建立一個新紀錄
			// 這裡做一次 copyUp = up 是為了避免指標指向同一個原始物件
			copyUp := up
			aggMap[key] = &copyUp
		}
	}

	// --- 第二階段：過濾與轉換 (Filtering) ---

	// 將 Map 轉回為 Slice (陣列)，方便後續排序
	var result []AccountUpdate
	for _, ptr := range aggMap {
		// 關鍵優化：如果加總後的變動是 0 (既沒有餘額變動，也沒有解鎖操作)
		// 則這筆資料不需要寫入資料庫，直接過濾掉，減少 DB IO 壓力
		if !ptr.Amount.IsZero() || !ptr.Unlock.IsZero() {
			result = append(result, *ptr)
		}
	}

	// --- 第三階段：排序 (Sorting) ---

	// 這是防死鎖最重要的一步 (Two-Phase Locking)
	// 無論是誰發起的交易，所有人的帳戶更新順序在這裡被強行統一
	sort.Slice(result, func(i, j int) bool {
		// 先比較 UserID 的字串大小
		if result[i].UserID.String() != result[j].UserID.String() {
			return result[i].UserID.String() < result[j].UserID.String()
		}
		// 如果是同一個人，再比較幣種名稱 (例如: BTC < USDT)
		return result[i].Currency < result[j].Currency
	})

	// 回傳最終可用於執行 SQL 更新的列表
	return result
}

// NewExchangeService 建立交易所服務
func NewExchangeService(
	orderRepo OrderRepository,
	accountRepo AccountRepository,
	tradeRepo TradeRepository,
	userRepo UserRepository,
	txManager DBTransaction,
	defaultSymbol string,
	tradeListener TradeEventListener,
	cacheRepo CacheRepository,     // 注入 CacheRepository
	eventBus EventPublisher,       // 注入 EventPublisher (Kafka，可為 nil)
	outboxRepo *outbox.Repository, // 注入 OutboxRepository（可為 nil，僅 Kafka 模式需要）
) *ExchangeServiceImpl {
	manager := matching.NewEngineManager()
	// 預先建立預設交易對的 Engine
	manager.GetEngine(defaultSymbol)
	return &ExchangeServiceImpl{
		orderRepo:     orderRepo,
		accountRepo:   accountRepo,
		tradeRepo:     tradeRepo,
		userRepo:      userRepo,
		tradeListener: tradeListener,
		txManager:     txManager,
		engineManager: manager,
		cacheRepo:     cacheRepo,
		eventBus:      eventBus,
		outboxRepo:    outboxRepo,
	}
}

// Ensure implementation
var _ ExchangeService = (*ExchangeServiceImpl)(nil)

// OnOrderBookUpdate 當訂單簿有變化時被呼叫
// [Redis 升級] 除了通知 Frontend，也非同步更新 Redis 快取
// [微服務升級] 若 tradeListener 為 nil（matching-engine 獨立進程），改透過 Kafka 通知 order-service 推播 WS
func (s *ExchangeServiceImpl) OnOrderBookUpdate(snapshot *matching.OrderBookSnapshot) {
	if s.cacheRepo != nil {
		go func() {
			ctxStore, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.cacheRepo.SetOrderBookSnapshot(ctxStore, snapshot); err != nil {
				logger.Error("更新 Redis 訂單簿快取失敗", zap.Error(err))
			}
		}()
	}

	if s.tradeListener != nil {
		// 同進程模式（單體 或 order-service 直接呼叫）
		s.tradeListener.OnOrderBookUpdate(snapshot)
	} else if s.eventBus != nil {
		// 微服務獨立進程模式：matching-engine 透過 Kafka 通知 order-service 推播 WS
		event := &OrderBookUpdatedEvent{
			EventType: EventOrderBookUpdated,
			Symbol:    snapshot.Symbol,
			Snapshot:  snapshot,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.eventBus.Publish(ctx, TopicOrderBook, snapshot.Symbol, event); err != nil {
			logger.Error("發布 OrderBookUpdatedEvent 至 Kafka 失敗", zap.Error(err))
		}
	}
}

// RestoreEngineSnapshot 伺服器冷啟動時，從資料庫載入活動訂單至記憶體引擎
func (s *ExchangeServiceImpl) RestoreEngineSnapshot(ctx context.Context) error {
	orders, err := s.orderRepo.GetActiveOrders(ctx)
	if err != nil {
		return fmt.Errorf("載入活動訂單失敗: %w", err)
	}

	for _, order := range orders {
		// 防呆機制：市價單絕對不能進撮合引擎的掛單簿
		if order.Type != TypeLimit {
			log.Printf("⚠️ 警告：嘗試恢復非限價單進入掛單簿，已忽略 (OrderID: %s)", order.ID)
			continue
		}

		engine := s.engineManager.GetEngine(order.Symbol)
		if engine != nil {
			// 轉換為撮合引擎格式，引擎內部的 Quantity 代表剩餘未成交數量
			matchingOrder := &matching.Order{
				ID:       order.ID,
				UserID:   order.UserID,
				Side:     matching.OrderSide(order.Side),
				Type:     matching.OrderType(order.Type),
				Price:    order.Price,
				Quantity: order.Quantity.Sub(order.FilledQuantity),
			}
			engine.RestoreOrder(matchingOrder)
		}
	}
	log.Printf("✅ 成功從資料庫恢復 %d 筆活動訂單至撮合引擎", len(orders))
	return nil
}

// CalculateTradeSettlement 計算單筆成交的資金變動
func (s *ExchangeServiceImpl) CalculateTradeSettlement(trade *matching.Trade, takerOrder, makerOrder *Order) ([]AccountUpdate, error) {
	tradeValue := trade.Price.Mul(trade.Quantity)

	var buyer, seller *Order
	if takerOrder.Side == SideBuy {
		buyer = takerOrder
		seller = makerOrder
	} else {
		buyer = makerOrder
		seller = takerOrder
	}

	base, quote, err := s.splitSymbol(takerOrder.Symbol)
	if err != nil {
		return nil, err
	}

	// 計算買方解鎖金額
	buyerUnlockAmount := tradeValue
	if buyer.ID == takerOrder.ID {
		if takerOrder.Type == TypeMarket {
			buyerUnlockAmount = tradeValue
		} else if !takerOrder.Price.IsZero() {
			buyerUnlockAmount = takerOrder.Price.Mul(trade.Quantity)
		}
	}

	buyerUnlockAmount = buyerUnlockAmount.Round(8)
	tradeValue = tradeValue.Round(8)
	tradeQty := trade.Quantity.Round(8)

	updates := []AccountUpdate{
		{UserID: buyer.UserID, Currency: quote, Amount: tradeValue.Neg(), Unlock: buyerUnlockAmount},
		{UserID: buyer.UserID, Currency: base, Amount: tradeQty, Unlock: decimal.Zero},
		{UserID: seller.UserID, Currency: base, Amount: tradeQty.Neg(), Unlock: tradeQty},
		{UserID: seller.UserID, Currency: quote, Amount: tradeValue, Unlock: decimal.Zero},
	}

	return updates, nil
}

// splitSymbol 將交易對拆分成 base 和 quote
func (s *ExchangeServiceImpl) splitSymbol(symbol string) (base, quote string, err error) {
	symbol = strings.ToUpper(symbol)
	parts := strings.Split(symbol, "-")
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("無效的交易對格式: %s", symbol)
}
