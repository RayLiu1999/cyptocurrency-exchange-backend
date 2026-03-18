package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// HandleSettlementEvent 是 Kafka exchange.settlements Topic 的消費者 Handler。
// 執行 TX2（原子結算）：更新訂單狀態、寫入成交記錄、結算資金。
// 實作冪等性：若成交記錄已存在則直接 Commit，避免重複結算。
func (s *ExchangeServiceImpl) HandleSettlementEvent(ctx context.Context, key, value []byte) error {
	var event SettlementRequestedEvent
	if err := json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 SettlementRequestedEvent 失敗: %w", err)
	}

	// === 冪等性保護：Consumer 至少一次語意下的重複交付防護 ===
	// 若有成交記錄，以第一筆 TradeID 作為冪等鍵；已存在則說明先前已成功結算
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
		// 無成交退款事件無法依賴 TradeID 判斷，改以 TakerOrder 狀態阻止重複退款。
		takerOrder, err := s.orderRepo.GetOrder(ctx, event.TakerOrderID)
		if err != nil {
			return fmt.Errorf("查詢 Taker 訂單失敗: %w", err)
		}
		if takerOrder.Status != StatusNew {
			logger.Info("無成交結算事件已處理，跳過",
				zap.String("order_id", event.TakerOrderID.String()),
				zap.String("status", StatusToString(takerOrder.Status)),
			)
			return nil
		}
	}

	return s.executeSettlementTx(ctx, &event)
}

// executeSettlementTx 執行原子結算事務（TX2）。
// 與 PlaceOrder 同步模式的 TX2 邏輯完全一致，確保資金安全與訂單狀態正確。
func (s *ExchangeServiceImpl) executeSettlementTx(ctx context.Context, event *SettlementRequestedEvent) error {
	var updatedOrders []*Order

	err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {

		// === Phase 1: 資源標準化排序與取得 Order 排他鎖 ===
		makerOrderIDsMap := make(map[uuid.UUID]bool)
		for _, trade := range event.Trades {
			makerOrderIDsMap[trade.MakerOrderID] = true
		}
		makerOrderIDsMap[event.TakerOrderID] = true

		var allOrderIDs []uuid.UUID
		for id := range makerOrderIDsMap {
			allOrderIDs = append(allOrderIDs, id)
		}

		// 依 UUID 字串字典序排序，確保任何並發情境下取鎖順序絕對一致（防死鎖）
		sort.Slice(allOrderIDs, func(i, j int) bool {
			return allOrderIDs[i].String() < allOrderIDs[j].String()
		})

		lockedOrders := make(map[uuid.UUID]*Order)
		for _, id := range allOrderIDs {
			lockedOrder, err := s.orderRepo.GetOrderForUpdate(ctx, id)
			if err != nil {
				return fmt.Errorf("鎖定訂單失敗 (ID: %s): %w", id, err)
			}
			lockedOrders[id] = lockedOrder
		}

		takerOrder := lockedOrders[event.TakerOrderID]

		// === Phase 1.1: TX 內部冪等性檢查 (解決 TOCTOU 風險) ===
		// 取得排他鎖後，再次確認訂單狀態。如果不是 NEW，說明已被同步或其他 Worker 處理完成。
		if takerOrder.Status != StatusNew {
			logger.Info("結算事件已在 TX 內偵測為重複，跳過",
				zap.String("order_id", takerOrder.ID.String()),
				zap.String("current_status", StatusToString(takerOrder.Status)),
			)
			return ErrIdempotencySkip
		}

		// 如果有成交紀錄，再次確認 Trade 是否已存在
		if len(event.Trades) > 0 {
			exists, err := s.tradeRepo.TradeExistsByID(ctx, event.Trades[0].ID)
			if err != nil {
				return fmt.Errorf("TX 內部冪等檢查失敗: %w", err)
			}
			if exists {
				logger.Info("成交紀錄已處理，跳過以避免重複結算",
					zap.String("trade_id", event.Trades[0].ID.String()),
				)
				return ErrIdempotencySkip
			}
		}

		// === Phase 2: 狀態計算與資金聚合 ===
		allAccountUpdates := make([]AccountUpdate, 0)

		for _, trade := range event.Trades {
			makerOrder := lockedOrders[trade.MakerOrderID]

			// 更新 Maker 成交量與狀態
			makerOrder.FilledQuantity = makerOrder.FilledQuantity.Add(trade.Quantity)
			if makerOrder.FilledQuantity.Equal(makerOrder.Quantity) {
				makerOrder.Status = StatusFilled
			} else if makerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
				makerOrder.Status = StatusPartiallyFilled
			}
			makerOrder.UpdatedAt = time.Now().UnixMilli()

			updates, err := s.CalculateTradeSettlement(trade, takerOrder, makerOrder)
			if err != nil {
				return fmt.Errorf("計算資金結算失敗: %w", err)
			}
			allAccountUpdates = append(allAccountUpdates, updates...)

			// 在 CalculateTradeSettlement 呼叫後才累加 Taker 成交量（確保函式收到 DB 原始狀態）
			takerOrder.FilledQuantity = takerOrder.FilledQuantity.Add(trade.Quantity)
		}

		// 根據最終成交量決定 Taker 訂單狀態，並計算應退還的保證金
		var refundAmount decimal.Decimal

		if takerOrder.Type == TypeMarket {
			if takerOrder.FilledQuantity.IsZero() {
				takerOrder.Status = StatusCanceled
			} else {
				takerOrder.Status = StatusFilled
			}
			// 市價單：退還未花完的預扣款（預扣 105% - 實際花費 = 退款）
			if takerOrder.Side == SideBuy {
				totalTradeValue := decimal.Zero
				for _, trade := range event.Trades {
					totalTradeValue = totalTradeValue.Add(trade.Price.Mul(trade.Quantity))
				}
				refundAmount = event.AmountLocked.Sub(totalTradeValue)
			} else {
				refundAmount = takerOrder.Quantity.Sub(takerOrder.FilledQuantity)
			}
		} else {
			// 限價單：依成交比例判斷狀態
			if takerOrder.FilledQuantity.Equal(takerOrder.Quantity) {
				takerOrder.Status = StatusFilled
			} else if event.RemainingQty.IsZero() {
				// STP 觸發：引擎將剩餘量歸零，沒有進入 OrderBook，視為已取消
				takerOrder.Status = StatusCanceled
				canceledQty := takerOrder.Quantity.Sub(takerOrder.FilledQuantity)
				if takerOrder.Side == SideBuy {
					refundAmount = canceledQty.Mul(takerOrder.Price)
				} else {
					refundAmount = canceledQty
				}
			} else if takerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
				takerOrder.Status = StatusPartiallyFilled
			}
		}

		if refundAmount.GreaterThan(decimal.Zero) {
			allAccountUpdates = append(allAccountUpdates, AccountUpdate{
				UserID:   takerOrder.UserID,
				Currency: event.LockedCurrency,
				Unlock:   refundAmount.Round(8),
			})
		}

		// === Phase 3: 依序執行 Database 寫入 ===

		// 1. 更新所有訂單狀態（依排序好的 ID 順序，確保取鎖一致）
		for _, id := range allOrderIDs {
			orderToSave := lockedOrders[id]
			orderToSave.UpdatedAt = time.Now().UnixMilli()
			if err := s.orderRepo.UpdateOrder(ctx, orderToSave); err != nil {
				return fmt.Errorf("更新訂單狀態失敗 (ID: %s): %w", id, err)
			}
			updatedOrders = append(updatedOrders, cloneOrder(orderToSave))
			if s.tradeListener != nil {
				s.tradeListener.OnOrderUpdate(orderToSave)
			}
		}

		// 2. 儲存所有成交記錄
		for _, trade := range event.Trades {
			if err := s.tradeRepo.CreateTrade(ctx, trade); err != nil {
				return fmt.Errorf("建立成交記錄失敗: %w", err)
			}
			if s.tradeListener != nil {
				s.tradeListener.OnTrade(trade)
			}
		}

		// 3. 聚合並排序資金變更後統一更新帳戶（防止相同 UserID 的死鎖）
		aggregatedUpdates := AggregateAndSortAccountUpdates(allAccountUpdates)
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
		if errors.Is(err, ErrIdempotencySkip) {
			return nil // 冪等性跳過，不視為錯誤，正常 Commit 為成功
		}
		logger.Error("結算事務失敗",
			zap.String("taker_order_id", event.TakerOrderID.String()),
			zap.Error(err),
		)
		return err
	}

	if s.eventBus != nil {
		for _, order := range updatedOrders {
			event := &OrderUpdatedEvent{
				EventType: EventOrderUpdated,
				Symbol:    order.Symbol,
				Order:     order,
			}
			publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if perr := s.eventBus.Publish(publishCtx, TopicOrderUpdates, order.Symbol, event); perr != nil {
				logger.Error("發布 OrderUpdatedEvent 失敗", zap.String("order_id", order.ID.String()), zap.Error(perr))
			}
			cancel()
		}
	}

	return nil
}

func cloneOrder(order *Order) *Order {
	if order == nil {
		return nil
	}
	copyOrder := *order
	return &copyOrder
}
