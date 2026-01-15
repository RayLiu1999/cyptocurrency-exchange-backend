package core

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ExchangeServiceImpl 交易所服務實作
type ExchangeServiceImpl struct {
	orderRepo      OrderRepository
	accountRepo    AccountRepository
	matchingEngine *matching.Engine
}

// NewExchangeService 建立交易所服務
func NewExchangeService(orderRepo OrderRepository, accountRepo AccountRepository, symbol string) *ExchangeServiceImpl {
	return &ExchangeServiceImpl{
		orderRepo:      orderRepo,
		accountRepo:    accountRepo,
		matchingEngine: matching.NewEngine(symbol),
	}
}

// Ensure implementation
var _ ExchangeService = (*ExchangeServiceImpl)(nil)

// PlaceOrder 處理下單請求
func (s *ExchangeServiceImpl) PlaceOrder(ctx context.Context, order *Order) error {
	// 1. 驗證訂單
	if order.Quantity.LessThanOrEqual(decimal.Zero) || order.Price.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("訂單數量或價格無效")
	}

	// 2. 鎖定資金
	currencyToLock, amountToLock := s.calculateLockAmount(order)

	err := s.accountRepo.LockFunds(ctx, order.UserID, currencyToLock, amountToLock)
	if err != nil {
		return fmt.Errorf("餘額不足: %w", err)
	}

	// 3. 建立訂單
	order.ID = uuid.New()
	order.Status = StatusNew
	order.FilledQuantity = decimal.Zero
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()

	err = s.orderRepo.CreateOrder(ctx, order)
	if err != nil {
		_ = s.accountRepo.UnlockFunds(ctx, order.UserID, currencyToLock, amountToLock)
		return fmt.Errorf("建立訂單失敗: %w", err)
	}

	// 4. 撮合
	matchOrder := s.convertToMatchingOrder(order)
	trades := s.matchingEngine.Process(matchOrder)

	// 5. 處理成交結果
	for _, trade := range trades {
		if err := s.ProcessTrade(ctx, trade, order); err != nil {
			log.Printf("處理成交失敗: %v", err)
			// TODO: 實作補償機制
		}
	}

	// 6. 更新訂單狀態
	filledQty := decimal.Zero
	for _, trade := range trades {
		filledQty = filledQty.Add(trade.Quantity)
	}
	order.FilledQuantity = filledQty

	if order.FilledQuantity.Equal(order.Quantity) {
		order.Status = StatusFilled
	} else if order.FilledQuantity.GreaterThan(decimal.Zero) {
		order.Status = StatusPartiallyFilled
	}

	order.UpdatedAt = time.Now()
	if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
		log.Printf("更新訂單狀態失敗: %v", err)
	}

	return nil
}

// calculateLockAmount 計算需要鎖定的資金
func (s *ExchangeServiceImpl) calculateLockAmount(order *Order) (currency string, amount decimal.Decimal) {
	// BUY: 鎖定報價貨幣 (如 USD)
	// SELL: 鎖定基礎貨幣 (如 BTC)
	if order.Side == SideBuy {
		return "USD", order.Price.Mul(order.Quantity)
	}
	return "BTC", order.Quantity
}

// convertToMatchingOrder 轉換為撮合引擎訂單
func (s *ExchangeServiceImpl) convertToMatchingOrder(order *Order) *matching.Order {
	var side matching.OrderSide
	if order.Side == SideBuy {
		side = matching.SideBuy
	} else {
		side = matching.SideSell
	}

	matchOrder := matching.NewOrder(side, order.Price, order.Quantity)
	matchOrder.ID = order.ID
	return matchOrder
}

// ProcessTrade 處理成交結果
func (s *ExchangeServiceImpl) ProcessTrade(ctx context.Context, trade *matching.Trade, takerOrder *Order) error {
	log.Printf("成交: 價格=%s, 數量=%s, Maker=%s, Taker=%s",
		trade.Price, trade.Quantity, trade.MakerOrderID, trade.TakerOrderID)

	// 1. 取得 Maker 訂單
	makerOrder, err := s.orderRepo.GetOrder(ctx, trade.MakerOrderID)
	if err != nil {
		return fmt.Errorf("取得 Maker 訂單失敗: %w", err)
	}

	// 2. 更新 Maker filled_quantity
	makerOrder.FilledQuantity = makerOrder.FilledQuantity.Add(trade.Quantity)

	// 3. 更新 Maker 狀態
	if makerOrder.FilledQuantity.Equal(makerOrder.Quantity) {
		makerOrder.Status = StatusFilled
	} else if makerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
		makerOrder.Status = StatusPartiallyFilled
	}

	makerOrder.UpdatedAt = time.Now()

	// 4. 儲存 Maker 訂單
	if err := s.orderRepo.UpdateOrder(ctx, makerOrder); err != nil {
		return fmt.Errorf("更新 Maker 訂單失敗: %w", err)
	}

	// 5. 結算資金
	if err := s.SettleTrade(ctx, trade, takerOrder, makerOrder); err != nil {
		return fmt.Errorf("結算失敗: %w", err)
	}

	return nil
}

// SettleTrade 結算資金
func (s *ExchangeServiceImpl) SettleTrade(ctx context.Context, trade *matching.Trade, takerOrder, makerOrder *Order) error {
	tradeValue := trade.Price.Mul(trade.Quantity)

	// 買方：解鎖報價貨幣 (USD)，獲得基礎貨幣 (BTC)
	// 賣方：解鎖基礎貨幣 (BTC)，獲得報價貨幣 (USD)

	var buyer, seller *Order
	if takerOrder.Side == SideBuy {
		buyer = takerOrder
		seller = makerOrder
	} else {
		buyer = makerOrder
		seller = takerOrder
	}

	// 買方結算
	if err := s.accountRepo.UnlockFunds(ctx, buyer.UserID, "USD", tradeValue); err != nil {
		return fmt.Errorf("解鎖買方 USD 失敗: %w", err)
	}
	if err := s.accountRepo.UpdateBalance(ctx, buyer.UserID, "BTC", trade.Quantity); err != nil {
		return fmt.Errorf("增加買方 BTC 失敗: %w", err)
	}

	// 賣方結算
	if err := s.accountRepo.UnlockFunds(ctx, seller.UserID, "BTC", trade.Quantity); err != nil {
		return fmt.Errorf("解鎖賣方 BTC 失敗: %w", err)
	}
	if err := s.accountRepo.UpdateBalance(ctx, seller.UserID, "USD", tradeValue); err != nil {
		return fmt.Errorf("增加賣方 USD 失敗: %w", err)
	}

	return nil
}

// GetOrder 取得訂單
func (s *ExchangeServiceImpl) GetOrder(ctx context.Context, id uuid.UUID) (*Order, error) {
	return s.orderRepo.GetOrder(ctx, id)
}

// GetOrdersByUser 取得用戶所有訂單
func (s *ExchangeServiceImpl) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error) {
	return s.orderRepo.GetOrdersByUser(ctx, userID)
}
