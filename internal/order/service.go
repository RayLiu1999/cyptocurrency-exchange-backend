package order

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/infrastructure/outbox"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Service 實作 OrderService，處理訂單生命週期與資金凍結
type Service struct {
	orderRepo   OrderRepository
	accountRepo AccountRepository
	userRepo    UserRepository
	tradeRepo   TradeRepository
	txManager   DBTransaction
	cacheRepo   domain.CacheRepository
	eventBus    domain.EventPublisher
	outboxRepo  *outbox.Repository
}

func NewService(
	orderRepo OrderRepository,
	accountRepo AccountRepository,
	userRepo UserRepository,
	tradeRepo TradeRepository,
	txManager DBTransaction,
	cacheRepo domain.CacheRepository,
	eventBus domain.EventPublisher,
	outboxRepo *outbox.Repository,
) *Service {
	return &Service{
		orderRepo:   orderRepo,
		accountRepo: accountRepo,
		userRepo:    userRepo,
		tradeRepo:   tradeRepo,
		txManager:   txManager,
		cacheRepo:   cacheRepo,
		eventBus:    eventBus,
		outboxRepo:  outboxRepo,
	}
}

func (s *Service) PlaceOrder(ctx context.Context, order *domain.Order) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveOrder("async", domain.SideToString(order.Side), domain.TypeToString(order.Type), err, time.Since(start))
	}()

	order.Symbol = strings.ToUpper(order.Symbol)
	order.Price = order.Price.Round(8)
	order.Quantity = order.Quantity.Round(8)

	if order.Quantity.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("訂單數量無效")
	}
	if order.Type == domain.TypeLimit && order.Price.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("限價單價格無效")
	}

	currencyToLock, amountToLock, err := s.calculateLockAmount(order)
	if err != nil {
		return fmt.Errorf("無法鎖定資金: %w", err)
	}

	newID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("產生訂單 ID 失敗: %w", err)
	}
	order.ID = newID
	order.Status = domain.StatusNew
	order.FilledQuantity = decimal.Zero
	now := time.Now().UnixMilli()
	order.CreatedAt = now
	order.UpdatedAt = now

	// === 第一事務: 鎖定資金 + 建立訂單 + Outbox ===
	err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if err := s.accountRepo.LockFunds(ctx, order.UserID, currencyToLock, amountToLock); err != nil {
			return fmt.Errorf("餘額不足: %w", err)
		}
		if err := s.orderRepo.CreateOrder(ctx, order); err != nil {
			return fmt.Errorf("建立訂單失敗: %w", err)
		}

		if !domain.IsSymbolAllowed(order.Symbol) {
			return fmt.Errorf("不允許的交易對: %s", order.Symbol)
		}

		if s.outboxRepo != nil && s.eventBus != nil {
			event := &domain.OrderPlacedEvent{
				EventType:      domain.EventOrderPlaced,
				Symbol:         order.Symbol,
				OrderID:        order.ID,
				UserID:         order.UserID,
				Side:           order.Side,
				Type:           order.Type,
				Price:          order.Price,
				Quantity:       order.Quantity,
				CreatedAt:      order.CreatedAt,
				AmountLocked:   amountToLock,
				LockedCurrency: currencyToLock,
			}
			payload, marshalErr := outbox.MarshalPayload(event)
			if marshalErr != nil {
				return fmt.Errorf("序列化 OrderPlacedEvent 失敗: %w", marshalErr)
			}
			if insertErr := s.outboxRepo.Insert(ctx, &outbox.Message{
				AggregateID:   order.ID.String(),
				AggregateType: "order_placed",
				Topic:         domain.TopicOrders,
				PartitionKey:  order.Symbol,
				Payload:       payload,
			}); insertErr != nil {
				return fmt.Errorf("寫入 Outbox 訊息失敗: %w", insertErr)
			}
		}

		return nil
	})

	return err
}

func (s *Service) CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error {
	orderPreCheck, err := s.orderRepo.GetOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("訂單不存在: %w", err)
	}
	if orderPreCheck.UserID != userID {
		return fmt.Errorf("權限不足")
	}
	if orderPreCheck.Status == domain.StatusFilled || orderPreCheck.Status == domain.StatusCanceled {
		return fmt.Errorf("無法取消已完成或已取消的訂單")
	}

	err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, orderID)
		if err != nil {
			return fmt.Errorf("鎖定訂單失敗: %w", err)
		}

		if order.Status == domain.StatusFilled || order.Status == domain.StatusCanceled {
			return fmt.Errorf("訂單已被撮合或取消，無法再次取消")
		}

		if s.outboxRepo != nil {
			event := &domain.OrderCancelRequestedEvent{
				EventType: domain.EventOrderCancelRequested,
				Symbol:    order.Symbol,
				OrderID:   orderID,
				Side:      order.Side,
				UserID:    order.UserID,
			}
			payload, marshalErr := outbox.MarshalPayload(event)
			if marshalErr != nil {
				return fmt.Errorf("序列化 OrderCancelRequestedEvent 失敗: %w", marshalErr)
			}
			if insertErr := s.outboxRepo.Insert(ctx, &outbox.Message{
				AggregateID:   order.ID.String(),
				AggregateType: "order_cancel_requested",
				Topic:         domain.TopicOrders,
				PartitionKey:  order.Symbol,
				Payload:       payload,
			}); insertErr != nil {
				return fmt.Errorf("寫入 Outbox 訊息失敗: %w", insertErr)
			}
		}

		return nil
	})

	return err
}

func (s *Service) GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	return s.orderRepo.GetOrder(ctx, id)
}

func (s *Service) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Order, error) {
	return s.orderRepo.GetOrdersByUser(ctx, userID)
}

func (s *Service) splitSymbol(symbol string) (base string, quote string, err error) {
	parts := strings.Split(symbol, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("無效的交易對格式")
	}
	return parts[0], parts[1], nil
}

func (s *Service) calculateLockAmount(order *domain.Order) (currency string, amount decimal.Decimal, err error) {
	base, quote, err := s.splitSymbol(order.Symbol)
	if err != nil {
		return "", decimal.Zero, err
	}
	if order.Side == domain.SideBuy {
		if order.Type == domain.TypeLimit {
			return quote, order.Price.Mul(order.Quantity), nil
		}
		estimatedFunds, err := s.estimateMarketBuyFunds(order.Symbol, order.Quantity)
		if err != nil {
			return "", decimal.Zero, fmt.Errorf("市價單預估資金失敗，可能因為快取不可用或流動性不足: %w", err)
		}
		lockedFunds := estimatedFunds.Mul(decimal.NewFromFloat(1.05))
		return quote, lockedFunds, nil
	}
	return base, order.Quantity, nil
}

func (s *Service) estimateMarketBuyFunds(symbol string, quantity decimal.Decimal) (decimal.Decimal, error) {
	if s.cacheRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snapshot, err := s.cacheRepo.GetOrderBookSnapshot(ctx, symbol)
		if err == nil && snapshot != nil && len(snapshot.Asks) > 0 {
			return estimateFromSnapshotAsks(snapshot.Asks, quantity)
		}
		return decimal.Zero, fmt.Errorf("Redis 訂單簿快取未命中或無流動性, %v", err)
	}
	return decimal.Zero, fmt.Errorf("cacheRepo 未初始化，無法估算市價單")
}

func estimateFromSnapshotAsks(asks []engine.OrderBookLevel, quantity decimal.Decimal) (decimal.Decimal, error) {
	remainingQty := quantity
	totalCost := decimal.Zero
	for _, ask := range asks {
		matchQty := remainingQty
		if ask.Quantity.LessThan(matchQty) {
			matchQty = ask.Quantity
		}
		totalCost = totalCost.Add(ask.Price.Mul(matchQty))
		remainingQty = remainingQty.Sub(matchQty)
		if remainingQty.IsZero() {
			return totalCost, nil
		}
	}
	return decimal.Zero, fmt.Errorf("insufficient liquidity to fulfill market buy (remaining: %s)", remainingQty)
}

func (s *Service) calculateUnlockAmount(order *domain.Order, remainingQty decimal.Decimal) (currency string, amount decimal.Decimal, err error) {
	base, quote, err := s.splitSymbol(order.Symbol)
	if err != nil {
		return "", decimal.Zero, err
	}
	if order.Side == domain.SideBuy {
		return quote, order.Price.Mul(remainingQty), nil
	}
	return base, remainingQty, nil
}
