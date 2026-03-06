package core

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PlaceOrder 處理下單請求
func (s *ExchangeServiceImpl) PlaceOrder(ctx context.Context, order *Order) error {
	order.Symbol = strings.ToUpper(order.Symbol)
	order.Price = order.Price.Round(8)
	order.Quantity = order.Quantity.Round(8)

	if order.Quantity.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("訂單數量無效")
	}
	if order.Type == TypeLimit && order.Price.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("限價單價格無效")
	}

	currencyToLock, amountToLock, err := s.calculateLockAmount(order)
	if err != nil {
		return fmt.Errorf("無法鎖定資金: %w", err)
	}

	order.ID = uuid.New()
	order.Status = StatusNew
	order.FilledQuantity = decimal.Zero
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()

	err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if err := s.accountRepo.LockFunds(ctx, order.UserID, currencyToLock, amountToLock); err != nil {
			return fmt.Errorf("餘額不足: %w", err)
		}
		if err := s.orderRepo.CreateOrder(ctx, order); err != nil {
			return fmt.Errorf("建立訂單失敗: %w", err)
		}
		return nil
	})

	if err != nil {
		return err
	}

	matchOrder := s.convertToMatchingOrder(order)
	engine := s.engineManager.GetEngine(order.Symbol)
	trades := engine.Process(matchOrder)

	for _, trade := range trades {
		err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {
			if err := s.ProcessTrade(ctx, trade, order); err != nil {
				return fmt.Errorf("處理成交失敗: %w", err)
			}
			return nil
		})
		if err != nil {
			log.Printf("成交處理失敗: %v", err)
		}
	}

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

	if order.Type == TypeMarket {
		if order.FilledQuantity.IsZero() {
			order.Status = StatusCanceled
		} else {
			order.Status = StatusFilled
		}

		refundCurrency := currencyToLock
		var refundAmount decimal.Decimal

		if order.Side == SideBuy {
			totalTradeValue := decimal.Zero
			for _, trade := range trades {
				totalTradeValue = totalTradeValue.Add(trade.Price.Mul(trade.Quantity))
			}
			refundAmount = amountToLock.Sub(totalTradeValue)
		} else {
			refundAmount = order.Quantity.Sub(order.FilledQuantity)
		}

		refundAmount = refundAmount.Round(8)

		if refundAmount.GreaterThan(decimal.Zero) {
			err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {
				if err := s.accountRepo.UnlockFunds(ctx, order.UserID, refundCurrency, refundAmount); err != nil {
					return fmt.Errorf("解鎖市價單剩餘資金失敗: %w", err)
				}
				return nil
			})
			if err != nil {
				log.Printf("市價單退款失敗: %v", err)
			}
		}
	}

	order.UpdatedAt = time.Now()
	if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
		log.Printf("更新訂單狀態失敗: %v", err)
	}

	if s.tradeListener != nil {
		s.tradeListener.OnOrderUpdate(order)
	}

	return nil
}

// CancelOrder 取消訂單
func (s *ExchangeServiceImpl) CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error {
	order, err := s.orderRepo.GetOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("訂單不存在: %w", err)
	}

	if order.UserID != userID {
		return fmt.Errorf("權限不足")
	}

	if order.Status == StatusFilled || order.Status == StatusCanceled {
		return fmt.Errorf("無法取消已完成或已取消的訂單")
	}

	remainingQty := order.Quantity.Sub(order.FilledQuantity)
	currency, amountToUnlock := s.calculateUnlockAmount(order, remainingQty)

	err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if err := s.accountRepo.UnlockFunds(ctx, order.UserID, currency, amountToUnlock); err != nil {
			return fmt.Errorf("解鎖資金失敗: %w", err)
		}

		order.Status = StatusCanceled
		order.UpdatedAt = time.Now()

		if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
			return fmt.Errorf("更新訂單狀態失敗: %w", err)
		}

		if s.tradeListener != nil {
			s.tradeListener.OnOrderUpdate(order)
		}

		engine := s.engineManager.GetEngine(order.Symbol)
		var matchSide matching.OrderSide
		if order.Side == SideBuy {
			matchSide = matching.SideBuy
		} else {
			matchSide = matching.SideSell
		}
		engine.Cancel(order.ID, matchSide)

		return nil
	})

	return err
}

// GetOrder 取得單一訂單
func (s *ExchangeServiceImpl) GetOrder(ctx context.Context, id uuid.UUID) (*Order, error) {
	return s.orderRepo.GetOrder(ctx, id)
}

// GetOrdersByUser 取得用戶訂單列表
func (s *ExchangeServiceImpl) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error) {
	return s.orderRepo.GetOrdersByUser(ctx, userID)
}

// calculateLockAmount 計算鎖定資金
func (s *ExchangeServiceImpl) calculateLockAmount(order *Order) (currency string, amount decimal.Decimal, err error) {
	base, quote := s.splitSymbol(order.Symbol)
	if order.Side == SideBuy {
		if order.Type == TypeLimit {
			return quote, order.Price.Mul(order.Quantity), nil
		}
		engine := s.engineManager.GetEngine(order.Symbol)
		estimatedFunds, err := engine.EstimateMarketBuyRequiredFunds(order.Quantity)
		if err != nil {
			return "", decimal.Zero, fmt.Errorf("市價單預估資金失敗: %w", err)
		}
		lockedFunds := estimatedFunds.Mul(decimal.NewFromFloat(1.05))
		return quote, lockedFunds, nil
	}
	return base, order.Quantity, nil
}

// calculateUnlockAmount 計算解鎖資金
func (s *ExchangeServiceImpl) calculateUnlockAmount(order *Order, remainingQty decimal.Decimal) (currency string, amount decimal.Decimal) {
	base, quote := s.splitSymbol(order.Symbol)
	if order.Side == SideBuy {
		return quote, order.Price.Mul(remainingQty)
	}
	return base, remainingQty
}

// convertToMatchingOrder 轉換為匹配訂單
func (s *ExchangeServiceImpl) convertToMatchingOrder(order *Order) *matching.Order {
	var side matching.OrderSide
	if order.Side == SideBuy {
		side = matching.SideBuy
	} else {
		side = matching.SideSell
	}

	var matchOrder *matching.Order
	if order.Type == TypeMarket {
		matchOrder = matching.NewMarketOrder(order.ID, order.UserID, side, order.Quantity)
	} else {
		matchOrder = matching.NewOrder(order.ID, order.UserID, side, order.Price, order.Quantity)
	}

	matchOrder.ID = order.ID
	return matchOrder
}
