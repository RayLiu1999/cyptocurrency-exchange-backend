package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PlaceOrder 處理下單請求
// 架構修正：
// 1. 先進行 DB 事務（鎖定資金、儲存訂單）
// 2. DB 事務成功後才送入記憶體引擎（避免裂腦問題）
// 3. 所有成交結算 + Taker 訂單最終更新，封裝在同一個原子事務（Atomic Taker Update）
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

	// 使用 UUID v7（含時間戳，保證 B-Tree 遞增寫入，消除索引碎片化）
	newID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("產生訂單 ID 失敗: %w", err)
	}
	order.ID = newID
	order.Status = StatusNew
	order.FilledQuantity = decimal.Zero
	now := time.Now().UnixMilli()
	order.CreatedAt = now
	order.UpdatedAt = now

	// === 第一個事務: 鎖定資金 + 建立訂單（寫入 DB，確保資金充足）===
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

	// === DB 成功後才送入記憶體引擎（解決 Commit Timing Anomaly/裂腦問題）===
	matchOrder := s.convertToMatchingOrder(order)
	engine := s.engineManager.GetEngine(order.Symbol)
	trades := engine.Process(matchOrder)

	// 計算 Taker 訂單最終狀態
	filledQty := decimal.Zero
	for _, trade := range trades {
		filledQty = filledQty.Add(trade.Quantity)
	}
	order.FilledQuantity = filledQty

	if order.Type == TypeMarket {
		if order.FilledQuantity.IsZero() {
			order.Status = StatusCanceled
		} else {
			order.Status = StatusFilled
		}
	} else {
		if order.FilledQuantity.Equal(order.Quantity) {
			order.Status = StatusFilled
		} else if order.FilledQuantity.GreaterThan(decimal.Zero) {
			order.Status = StatusPartiallyFilled
		}
	}

	// === 第二個事務（Atomic Taker Update）: 所有成交結算 + Taker 訂單更新，封裝在同一個原子操作 ===
	// 這樣即使中途崩潰，不會出現「成交了但訂單還顯示 NEW」的雙重花費漏洞
	if len(trades) > 0 || order.Type == TypeMarket {
		err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
			// 結算所有成交
			for _, trade := range trades {
				if err := s.ProcessTrade(ctx, trade, order); err != nil {
					return fmt.Errorf("處理成交失敗: %w", err)
				}
			}

			// 市價單退還未成交的保證金
			if order.Type == TypeMarket {
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
					if err := s.accountRepo.UnlockFunds(ctx, order.UserID, refundCurrency, refundAmount); err != nil {
						return fmt.Errorf("解鎖市價單剩餘資金失敗: %w", err)
					}
				}
			}

			// Taker 訂單最終狀態更新（與結算在同一事務）
			order.UpdatedAt = time.Now().UnixMilli()
			if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
				return fmt.Errorf("更新 Taker 訂單狀態失敗: %w", err)
			}

			return nil
		})
		if err != nil {
			log.Printf("原子結算事務失敗: %v", err)
		}
	} else {
		// 無成交：只更新訂單時間戳
		order.UpdatedAt = time.Now().UnixMilli()
		if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
			log.Printf("更新訂單狀態失敗: %v", err)
		}
	}

	if s.tradeListener != nil {
		s.tradeListener.OnOrderUpdate(order)
	}

	return nil
}

// CancelOrder 取消訂單（使用 FOR UPDATE 防止與 ProcessTrade 的競態條件）
func (s *ExchangeServiceImpl) CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error {
	// 先做一次不加鎖的查詢，用於權限與狀態的快速校驗
	orderPreCheck, err := s.orderRepo.GetOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("訂單不存在: %w", err)
	}
	if orderPreCheck.UserID != userID {
		return fmt.Errorf("權限不足")
	}
	if orderPreCheck.Status == StatusFilled || orderPreCheck.Status == StatusCanceled {
		return fmt.Errorf("無法取消已完成或已取消的訂單")
	}

	err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		// 在事務內使用 FOR UPDATE 重新讀取，確保排他性
		order, err := s.orderRepo.GetOrderForUpdate(ctx, orderID)
		if err != nil {
			return fmt.Errorf("鎖定訂單失敗: %w", err)
		}

		// 再次驗證（可能在等待鎖的過程中已被撮合）
		if order.Status == StatusFilled || order.Status == StatusCanceled {
			return fmt.Errorf("訂單已被撮合或取消，無法再次取消")
		}

		remainingQty := order.Quantity.Sub(order.FilledQuantity)
		currency, amountToUnlock, err := s.calculateUnlockAmount(order, remainingQty)
		if err != nil {
			return fmt.Errorf("計算解鎖金額失敗: %w", err)
		}

		if err := s.accountRepo.UnlockFunds(ctx, order.UserID, currency, amountToUnlock); err != nil {
			return fmt.Errorf("解鎖資金失敗: %w", err)
		}

		order.Status = StatusCanceled
		order.UpdatedAt = time.Now().UnixMilli()

		if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
			return fmt.Errorf("更新訂單狀態失敗: %w", err)
		}

		if s.tradeListener != nil {
			s.tradeListener.OnOrderUpdate(order)
		}

		// 從記憶體引擎移除
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
	base, quote, err := s.splitSymbol(order.Symbol)
	if err != nil {
		return "", decimal.Zero, err
	}
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
func (s *ExchangeServiceImpl) calculateUnlockAmount(order *Order, remainingQty decimal.Decimal) (currency string, amount decimal.Decimal, err error) {
	base, quote, err := s.splitSymbol(order.Symbol)
	if err != nil {
		return "", decimal.Zero, err
	}
	if order.Side == SideBuy {
		return quote, order.Price.Mul(remainingQty), nil
	}
	return base, remainingQty, nil
}

// convertToMatchingOrder 轉換為匹配引擎訂單
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

// errIsNotFound 檢查是否為記錄找不到的錯誤（用於內部判斷）
func errIsNotFound(err error) bool {
	return errors.Is(err, errors.New("no rows in result set"))
}
