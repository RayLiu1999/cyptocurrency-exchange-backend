package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

var ErrIdempotencySkip = errors.New("idempotency skip")
var ErrStaleSettlementEvent = errors.New("stale settlement event")

// AccountUpdate 資金變更紀錄
type AccountUpdate struct {
	UserID   uuid.UUID
	Currency string
	Amount   decimal.Decimal
	Unlock   decimal.Decimal
}

// EventSubscriber 是負責處理 Kafka 事件的独立服務 (CQRS - 讀寫分離下的非同部事件處理)
type EventSubscriber struct {
	orderRepo   OrderRepository
	accountRepo AccountRepository
	tradeRepo   TradeRepository
	// txManager 切分兩個职責:
	// 1. ExecTx：開啟資料庫事務并將 Tx 注入 Context
	// 2. ValidateFencingTokenTx：在 Tx 內部驗證 Token，是防腦裂的最後一道鐵閘
	txManager   DBTransaction
	eventBus    domain.EventPublisher
}

func NewEventSubscriber(
	orderRepo OrderRepository,
	accountRepo AccountRepository,
	tradeRepo TradeRepository,
	txManager DBTransaction,
	eventBus domain.EventPublisher,
) *EventSubscriber {
	return &EventSubscriber{
		orderRepo:   orderRepo,
		accountRepo: accountRepo,
		tradeRepo:   tradeRepo,
		txManager:   txManager,
		eventBus:    eventBus,
	}
}

// HandleEvents 是 Kafka exchange.settlements Topic 的消費者 Handler。
// 執行 TX2（原子結算）：更新訂單狀態、寫入成交記錄、結算資金、處理撤單確認。
// 實作冪等性：若成交記錄已存在則直接 Commit，避免重複結算。
func (s *EventSubscriber) HandleEvents(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("settlement", "exchange.settlements", err, time.Since(start))
	}()

	var envelope struct {
		EventType domain.EventType `json:"event_type"`
	}
	if err := json.Unmarshal(value, &envelope); err != nil {
		return fmt.Errorf("解析結算事件失敗: %w", err)
	}

	switch envelope.EventType {
	case domain.EventSettlementRequested:
		var event domain.SettlementRequestedEvent
		if err = json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 SettlementRequestedEvent 失敗: %w", err)
		}
		return s.handleSettlementRequested(ctx, &event)

	case domain.EventOrderCanceled:
		var event domain.OrderCanceledEvent
		if err = json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 OrderCanceledEvent 失敗: %w", err)
		}
		return s.handleOrderCanceled(ctx, &event)

	default:
		logger.Warn("HandleSettlementEvent 收到未知 EventType", zap.String("event_type", string(envelope.EventType)))
		return nil
	}
}

// 處理結算請求事件
func (s *EventSubscriber) handleSettlementRequested(ctx context.Context, event *domain.SettlementRequestedEvent) error {

	// === 冪等性保護：Consumer 至少一次語意下的重複交付防護 ===
	if len(event.Trades) > 0 {
		exists, err := s.tradeRepo.TradeExistsByID(ctx, event.Trades[0].ID)
		if err != nil {
			return fmt.Errorf("冪等檢查失敗: %w", err)
		}
		if exists {
			logger.Info("結算事件已處理，跳過以避免重複結算",
				zap.String("trade_id", event.Trades[0].ID.String()),
			)
			return nil
		}
	} else {
		takerOrder, err := s.orderRepo.GetOrder(ctx, event.TakerOrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("收到過期的無成交結算事件，對應訂單不存在，直接跳過",
					zap.String("taker_order_id", event.TakerOrderID.String()),
				)
				return nil
			}
			return fmt.Errorf("查詢 Taker 訂單失敗: %w", err)
		}
		if takerOrder.Status != domain.StatusNew {
			logger.Info("無成交結算事件已處理，跳過",
				zap.String("order_id", event.TakerOrderID.String()),
				zap.String("status", domain.StatusToString(takerOrder.Status)),
			)
			return nil
		}
	}

	err := s.executeSettlementTx(ctx, event)
	if err == nil {
		metrics.AddTradesExecuted("async", len(event.Trades))
	}
	return err
}

func (s *EventSubscriber) handleOrderCanceled(ctx context.Context, event *domain.OrderCanceledEvent) error {
	var updatedOrder *domain.Order
	var orderSymbol string // Used for the updated event

	err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if event.FencingToken > 0 {
			valid, err := s.txManager.ValidateFencingTokenTx(ctx, "matching-engine:global", event.FencingToken)
			if err != nil {
				return fmt.Errorf("TX 內部驗證 FencingToken 失敗: %w", err)
			}
			if !valid {
				logger.Warn("⚠️ 殭屍訊息攔截：FencingToken 不符，此取消事件來自舊 Leader，已安全丟棄",
					zap.Int64("event_fencing_token", event.FencingToken),
					zap.String("order_id", event.OrderID.String()),
				)
				return ErrStaleSettlementEvent
			}
		}

		order, err := s.orderRepo.GetOrderForUpdate(ctx, event.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("收到過期的撤單結算事件，對應訂單不存在，直接跳過",
					zap.String("order_id", event.OrderID.String()),
				)
				return ErrStaleSettlementEvent
			}
			return fmt.Errorf("鎖定訂單失敗: %w", err)
		}

		if order.Status == domain.StatusFilled || order.Status == domain.StatusCanceled {
			logger.Info("結算事件已處理或訂單已完成，跳過", zap.String("order_id", order.ID.String()))
			return nil
		}

		remainingQty := order.Quantity.Sub(order.FilledQuantity)
		currency, amountToUnlock, err := s.calculateUnlockAmount(order, remainingQty)
		if err != nil {
			return fmt.Errorf("計算解鎖金額失敗: %w", err)
		}

		if amountToUnlock.GreaterThan(decimal.Zero) {
			if err := s.accountRepo.UnlockFunds(ctx, order.UserID, currency, amountToUnlock); err != nil {
				return fmt.Errorf("解鎖資金失敗: %w", err)
			}
		}

		order.Status = domain.StatusCanceled
		order.UpdatedAt = time.Now().UnixMilli()

		if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
			return fmt.Errorf("更新訂單狀態失敗: %w", err)
		}

		updatedOrder = cloneOrder(order)
		orderSymbol = order.Symbol
		return nil
	})
	if errors.Is(err, ErrStaleSettlementEvent) {
		return nil
	}

	// 若更新成功，廣播 OrderUpdatedEvent
	if err == nil && updatedOrder != nil && s.eventBus != nil {
		updateEvent := &domain.OrderUpdatedEvent{
			EventType: domain.EventOrderUpdated,
			Symbol:    orderSymbol,
			Order:     updatedOrder,
		}
		publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if publishErr := s.eventBus.Publish(publishCtx, domain.TopicOrderUpdates, orderSymbol, updateEvent); publishErr != nil {
			logger.Error("發布 OrderUpdatedEvent (Canceled) 失敗", zap.Error(publishErr))
		}
		cancel()
	}

	return err
}

func (s *EventSubscriber) executeSettlementTx(ctx context.Context, event *domain.SettlementRequestedEvent) error {
	var updatedOrders []*domain.Order

	err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {

		// ╔══════════════════════════════════════════════════════════════════╗
		// ║  🛡️  第一道保險箱：Fencing Token 原子驗證（終極防腦裂）         ║
		// ╠══════════════════════════════════════════════════════════════════╣
		// ║  此時我們已在 DB Transaction 內部。                              ║
		// ║  ValidateFencingTokenTx 執行：                                   ║
		// ║  SELECT fencing_token FROM partition_leader_locks FOR SHARE       ║
		// ║                                                                   ║
		// ║  FOR SHARE 的意義：在此 Tx Commit 前，                           ║
		// ║  任何想修改 partition_leader_locks（如新 Leader 上位）的事務     ║
		// ║  都會在資料庫層面被卡住，必須等待。                              ║
		// ║  → 即使 GC 導致本服務發呆 N 秒，殭屍訊息也絕不可能穿透。        ║
		// ╚══════════════════════════════════════════════════════════════════╝
		if event.FencingToken > 0 {
			valid, err := s.txManager.ValidateFencingTokenTx(ctx, "matching-engine:global", event.FencingToken)
			if err != nil {
				return fmt.Errorf("TX 內部驗證 FencingToken 失敗: %w", err)
			}
			if !valid {
				logger.Warn("⚠️ 殭屍訊息攔截：FencingToken 不符，此訊息來自舊 Leader，已安全丟棄",
					zap.Int64("event_fencing_token", event.FencingToken),
					zap.String("taker_order_id", event.TakerOrderID.String()),
				)
				// 回傳 ErrStaleSettlementEvent，讓外層跳過此訊息並正常 Commit Kafka Offset
				// 不回傳一般 error，避免 Consumer 重試無限循環
				return ErrStaleSettlementEvent
			}
		}

		makerOrderIDsMap := make(map[uuid.UUID]bool)
		for _, trade := range event.Trades {
			makerOrderIDsMap[trade.MakerOrderID] = true
		}
		makerOrderIDsMap[event.TakerOrderID] = true

		var allOrderIDs []uuid.UUID
		for id := range makerOrderIDsMap {
			allOrderIDs = append(allOrderIDs, id)
		}

		sort.Slice(allOrderIDs, func(i, j int) bool {
			return allOrderIDs[i].String() < allOrderIDs[j].String()
		})

		lockedOrders := make(map[uuid.UUID]*domain.Order)
		for _, id := range allOrderIDs {
			lockedOrder, err := s.orderRepo.GetOrderForUpdate(ctx, id)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					logger.Warn("收到過期的結算事件，引用了不存在的訂單，直接跳過以避免 poison message 無限重試",
						zap.String("taker_order_id", event.TakerOrderID.String()),
						zap.String("missing_order_id", id.String()),
						zap.Int("trade_count", len(event.Trades)),
					)
					return ErrStaleSettlementEvent
				}
				return fmt.Errorf("鎖定訂單失敗 (ID: %s): %w", id, err)
			}
			lockedOrders[id] = lockedOrder
		}

		takerOrder := lockedOrders[event.TakerOrderID]

		if takerOrder.Status != domain.StatusNew {
			return ErrIdempotencySkip
		}

		if len(event.Trades) > 0 {
			exists, err := s.tradeRepo.TradeExistsByID(ctx, event.Trades[0].ID)
			if err != nil {
				return fmt.Errorf("TX 內部冪等檢查失敗: %w", err)
			}
			if exists {
				return ErrIdempotencySkip
			}
		}

		allAccountUpdates := make([]AccountUpdate, 0)

		for _, trade := range event.Trades {
			makerOrder := lockedOrders[trade.MakerOrderID]

			// 更新 Maker 成交量與狀態
			makerOrder.FilledQuantity = makerOrder.FilledQuantity.Add(trade.Quantity)
			if makerOrder.FilledQuantity.Equal(makerOrder.Quantity) {
				makerOrder.Status = domain.StatusFilled
			} else if makerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
				makerOrder.Status = domain.StatusPartiallyFilled
			}
			makerOrder.UpdatedAt = time.Now().UnixMilli()

			updates, err := s.calculateTradeSettlement(trade, takerOrder, makerOrder)
			if err != nil {
				return fmt.Errorf("計算資金結算失敗: %w", err)
			}
			allAccountUpdates = append(allAccountUpdates, updates...)

			takerOrder.FilledQuantity = takerOrder.FilledQuantity.Add(trade.Quantity)
		}

		var refundAmount decimal.Decimal

		if takerOrder.Type == domain.TypeMarket {
			if takerOrder.FilledQuantity.IsZero() {
				takerOrder.Status = domain.StatusCanceled
			} else {
				takerOrder.Status = domain.StatusFilled
			}
			if takerOrder.Side == domain.SideBuy {
				totalTradeValue := decimal.Zero
				for _, trade := range event.Trades {
					totalTradeValue = totalTradeValue.Add(trade.Price.Mul(trade.Quantity))
				}
				refundAmount = event.AmountLocked.Sub(totalTradeValue)
			} else {
				refundAmount = takerOrder.Quantity.Sub(takerOrder.FilledQuantity)
			}
		} else {
			if takerOrder.FilledQuantity.Equal(takerOrder.Quantity) {
				takerOrder.Status = domain.StatusFilled
			} else if event.RemainingQty.IsZero() {
				takerOrder.Status = domain.StatusCanceled
				canceledQty := takerOrder.Quantity.Sub(takerOrder.FilledQuantity)
				if takerOrder.Side == domain.SideBuy {
					refundAmount = canceledQty.Mul(takerOrder.Price)
				} else {
					refundAmount = canceledQty
				}
			} else if takerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
				takerOrder.Status = domain.StatusPartiallyFilled
			}
		}

		if refundAmount.GreaterThan(decimal.Zero) {
			allAccountUpdates = append(allAccountUpdates, AccountUpdate{
				UserID:   takerOrder.UserID,
				Currency: event.LockedCurrency,
				Unlock:   refundAmount.Round(8),
			})
		}

		for _, id := range allOrderIDs {
			orderToSave := lockedOrders[id]
			orderToSave.UpdatedAt = time.Now().UnixMilli()
			if err := s.orderRepo.UpdateOrder(ctx, orderToSave); err != nil {
				return fmt.Errorf("更新訂單狀態失敗 (ID: %s): %w", id, err)
			}
			updatedOrders = append(updatedOrders, cloneOrder(orderToSave))
		}

		for _, trade := range event.Trades {
			if err := s.tradeRepo.CreateTrade(ctx, trade); err != nil {
				return fmt.Errorf("建立成交記錄失敗: %w", err)
			}
		}

		aggregatedUpdates := aggregateAndSortAccountUpdates(allAccountUpdates)
		for _, up := range aggregatedUpdates {
			if up.Unlock.GreaterThan(decimal.Zero) {
				if err := s.accountRepo.UnlockFunds(ctx, up.UserID, up.Currency, up.Unlock); err != nil {
					return fmt.Errorf("解鎖資金失敗 (%s %s): %w", up.UserID, up.Currency, err)
				}
			}
			if !up.Amount.IsZero() {
				if err := s.accountRepo.UpdateBalance(ctx, up.UserID, up.Currency, up.Amount); err != nil {
					return fmt.Errorf("更新餘額失敗 (%s %s): %w", up.UserID, up.Currency, err)
				}
			}
		}

		return nil
	})

	if err != nil {
		if errors.Is(err, ErrIdempotencySkip) || errors.Is(err, ErrStaleSettlementEvent) {
			return nil
		}
		logger.Error("結算事務失敗",
			zap.String("taker_order_id", event.TakerOrderID.String()),
			zap.Error(err),
		)
		return err
	}

	if s.eventBus != nil {
		for _, order := range updatedOrders {
			event := &domain.OrderUpdatedEvent{
				EventType: domain.EventOrderUpdated,
				Symbol:    order.Symbol,
				Order:     order,
			}
			publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if perr := s.eventBus.Publish(publishCtx, domain.TopicOrderUpdates, order.Symbol, event); perr != nil {
				logger.Error("發布 OrderUpdatedEvent 失敗", zap.String("order_id", order.ID.String()), zap.Error(perr))
			}
			cancel()
		}
	}

	return nil
}

func (s *EventSubscriber) calculateUnlockAmount(order *domain.Order, remainingQty decimal.Decimal) (currency string, amount decimal.Decimal, err error) {
	base, quote, err := splitSymbol(order.Symbol)
	if err != nil {
		return "", decimal.Zero, err
	}
	if order.Side == domain.SideBuy {
		return quote, order.Price.Mul(remainingQty), nil
	}
	return base, remainingQty, nil
}

func (s *EventSubscriber) calculateTradeSettlement(trade *engine.Trade, takerOrder, makerOrder *domain.Order) ([]AccountUpdate, error) {
	tradeValue := trade.Price.Mul(trade.Quantity)

	var buyer, seller *domain.Order
	if takerOrder.Side == domain.SideBuy {
		buyer = takerOrder
		seller = makerOrder
	} else {
		buyer = makerOrder
		seller = takerOrder
	}

	base, quote, err := splitSymbol(takerOrder.Symbol)
	if err != nil {
		return nil, err
	}

	buyerUnlockAmount := tradeValue
	if buyer.ID == takerOrder.ID {
		if takerOrder.Type == domain.TypeMarket {
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

func aggregateAndSortAccountUpdates(updates []AccountUpdate) []AccountUpdate {
	aggMap := make(map[string]*AccountUpdate)

	for _, up := range updates {
		key := up.UserID.String() + "_" + up.Currency

		if existing, ok := aggMap[key]; ok {
			existing.Amount = existing.Amount.Add(up.Amount)
			existing.Unlock = existing.Unlock.Add(up.Unlock)
		} else {
			copyUp := up
			aggMap[key] = &copyUp
		}
	}

	var result []AccountUpdate
	for _, ptr := range aggMap {
		if !ptr.Amount.IsZero() || !ptr.Unlock.IsZero() {
			result = append(result, *ptr)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].UserID.String() != result[j].UserID.String() {
			return result[i].UserID.String() < result[j].UserID.String()
		}
		return result[i].Currency < result[j].Currency
	})

	return result
}

func cloneOrder(o *domain.Order) *domain.Order {
	if o == nil {
		return nil
	}
	copyOrder := *o
	return &copyOrder
}
