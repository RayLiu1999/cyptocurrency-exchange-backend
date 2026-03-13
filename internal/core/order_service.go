package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PlaceOrder 處理下單請求
// 架構：
//  1. 第一事務：鎖定資金 + 建立訂單（DB 持久化，確保資金充足後才往下走）
//  2. 送入記憶體撮合引擎，取得成交列表（Trades）
//  3. 第二事務（Atomic Settlement）：Taker 與所有 Maker 的訂單統一排序鎖定、
//     結算資金、寫入成交記錄，避免 Lost Update 與死鎖
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

	// === 第一事務: 鎖定資金 + 建立訂單（寫入 DB，確保資金充足）===
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

	// === TX1 成功後，優先嘗試 Kafka 非同步撮合；無 Kafka 時退回同步模式（向後相容）===
	if s.eventBus != nil {
		event := &OrderPlacedEvent{
			EventType:      EventOrderPlaced,
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
		if err := s.eventBus.Publish(ctx, TopicOrders, order.Symbol, event); err != nil {
			// Kafka 發布失敗：記錄錯誤但不 rollback TX1（資金已鎖定，訂單已在 DB）
			// 此為已知的雙寫風險，Transactional Outbox 可在 Phase 6 補強
			log.Printf("發布 OrderPlacedEvent 失敗: %v", err)
		}
		return nil
	}

	// === 無 Kafka Fallback：維持原本的同步撮合邏輯 ===
	// DB Commit 成功後才送入記憶體引擎（解決 Commit Timing Anomaly / 裂腦問題）
	matchOrder := s.convertToMatchingOrder(order)
	engine := s.engineManager.GetEngine(order.Symbol)
	trades := engine.Process(matchOrder)

	// 判斷是否需要啟動結算事務：
	//   1. 有成交紀錄（Taker 吃到了 Maker）
	//   2. 市價單（即使沒成交也必須退還預扣款，並標記為 Canceled）
	//   3. 限價單觸發 STP（剩餘數量歸零，沒有進入 OrderBook，必須退還保證金）
	needsSettlement := len(trades) > 0 || order.Type == TypeMarket || matchOrder.Quantity.IsZero()

	// === 第二事務（Atomic Settlement）: 所有成交結算 + 訂單更新，封裝在同一個原子操作 ===
	// 這樣即使中途崩潰，不會出現「成交了但訂單還顯示 NEW」的雙重花費漏洞
	if needsSettlement {
		err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {

			// === Phase 1: 資源標準化排序與獲取 Order 鎖 ===
			// 把「Taker 自己」與「所有被吃到的 Maker」的 ID 統一放入排序池，
			// 確保任何並發情境下取鎖順序都一致，徹底杜絕 Orders 表死鎖
			makerOrderIDsMap := make(map[uuid.UUID]bool)
			for _, trade := range trades {
				makerOrderIDsMap[trade.MakerOrderID] = true
			}
			makerOrderIDsMap[order.ID] = true // Taker 本身也需要鎖定，防止 Lost Update

			var allOrderIDs []uuid.UUID
			for id := range makerOrderIDsMap {
				allOrderIDs = append(allOrderIDs, id)
			}

			// 依照 UUID 字串字典序排序，確保獲取鎖的順序絕對一致
			sort.Slice(allOrderIDs, func(i, j int) bool {
				return allOrderIDs[i].String() < allOrderIDs[j].String()
			})

			// 依序發出 SELECT ... FOR UPDATE，取得每張訂單的行鎖並讀取最新現況
			lockedOrders := make(map[uuid.UUID]*Order)
			for _, id := range allOrderIDs {
				lockedOrder, err := s.orderRepo.GetOrderForUpdate(ctx, id)
				if err != nil {
					return fmt.Errorf("鎖定訂單失敗 (ID: %s): %w", id, err)
				}
				lockedOrders[id] = lockedOrder
			}

			// 從已鎖定的 Map 中提取 Taker 的最新狀態（此時可安全讀取，DB 保證排他性）
			takerOrder := lockedOrders[order.ID]

			// === Phase 2: 狀態計算、資金聚合 ===
			allAccountUpdates := make([]AccountUpdate, 0)
			tradesToSave := make([]*matching.Trade, 0, len(trades))

			for _, trade := range trades {
				makerOrder := lockedOrders[trade.MakerOrderID]

				// 更新 Maker 訂單的成交數量與狀態
				makerOrder.FilledQuantity = makerOrder.FilledQuantity.Add(trade.Quantity)
				if makerOrder.FilledQuantity.Equal(makerOrder.Quantity) {
					makerOrder.Status = StatusFilled
				} else if makerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
					makerOrder.Status = StatusPartiallyFilled
				}
				makerOrder.UpdatedAt = time.Now().UnixMilli()

				// 計算這筆成交的資金結算（傳入 takerOrder 取得買賣方身分與限價單單價）
				updates, err := s.CalculateTradeSettlement(trade, takerOrder, makerOrder)
				if err != nil {
					return fmt.Errorf("計算資金結算失敗: %w", err)
				}
				allAccountUpdates = append(allAccountUpdates, updates...)
				tradesToSave = append(tradesToSave, trade)

				// 在 CalculateTradeSettlement 呼叫完畢後，再疊加 Taker 的成交量，
				// 確保函式接收到的 takerOrder 資料與 DB 原始狀態一致
				takerOrder.FilledQuantity = takerOrder.FilledQuantity.Add(trade.Quantity)
			}

			// 根據最終成交數量判斷 Taker 訂單狀態，並計算應退還的保證金
			var refundAmount decimal.Decimal

			if takerOrder.Type == TypeMarket {
				// 市價單：全部成交視為 Filled，否則視為 Canceled
				if takerOrder.FilledQuantity.IsZero() {
					takerOrder.Status = StatusCanceled
				} else {
					takerOrder.Status = StatusFilled
				}
				// 退還未花完的預扣款（預扣 105% - 實際花費 = 退款）
				if takerOrder.Side == SideBuy {
					totalTradeValue := decimal.Zero
					for _, trade := range trades {
						totalTradeValue = totalTradeValue.Add(trade.Price.Mul(trade.Quantity))
					}
					refundAmount = amountToLock.Sub(totalTradeValue)
				} else {
					refundAmount = takerOrder.Quantity.Sub(takerOrder.FilledQuantity)
				}
			} else {
				// 限價單：依成交比例判斷狀態
				if takerOrder.FilledQuantity.Equal(takerOrder.Quantity) {
					takerOrder.Status = StatusFilled
				} else if matchOrder.Quantity.IsZero() {
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

			// 將保證金退款加入帳戶更新陣列
			if refundAmount.GreaterThan(decimal.Zero) {
				allAccountUpdates = append(allAccountUpdates, AccountUpdate{
					UserID:   takerOrder.UserID,
					Currency: currencyToLock,
					Unlock:   refundAmount.Round(8),
				})
			}

			// === Phase 3: 依序執行 Database 寫入 ===

			// 1. 更新所有訂單狀態（Maker + Taker，依照排序好的 ID 順序）
			for _, id := range allOrderIDs {
				orderToSave := lockedOrders[id]
				orderToSave.UpdatedAt = time.Now().UnixMilli()
				if err := s.orderRepo.UpdateOrder(ctx, orderToSave); err != nil {
					return fmt.Errorf("更新訂單狀態失敗 (ID: %s): %w", id, err)
				}
				if s.tradeListener != nil {
					s.tradeListener.OnOrderUpdate(orderToSave)
				}
			}

			// 2. 儲存所有成交記錄
			for _, trade := range tradesToSave {
				if err := s.tradeRepo.CreateTrade(ctx, trade); err != nil {
					return fmt.Errorf("建立成交記錄失敗: %w", err)
				}
				if s.tradeListener != nil {
					s.tradeListener.OnTrade(trade)
				}
			}

			// 3. 聚合並排序資金變更後統一更新帳戶
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

			// 將鎖定後讀取到的最新狀態回寫給外層 order 指標，供後續推播使用
			*order = *takerOrder

			return nil
		})

		if err != nil {
			log.Printf("原子結算事務失敗: %v", err)
		}
	} else {
		// 無成交：限價單已在第一事務（CreateOrder）完整寫入 DB，進入 OrderBook 排隊等候 Maker。
		// ⚠️ 絕對不可在此呼叫 UpdateOrder！
		// 此時 order 是建立前的「舊快照」(Status=NEW)，而這張單現在是 Maker，
		// 隨時可能被其他 Goroutine 的 Settlement Tx 成交並更新為 FILLED。
		// 若在此裸呼叫 UpdateOrder，會把 FILLED 狀態硬蓋回 NEW（Lost Update 復燃）。
		if s.tradeListener != nil {
			s.tradeListener.OnOrderUpdate(order)
		}
	}

	// 掛單簿已變更（新增/成交），推播最新深度給所有連線客戶端
	if s.tradeListener != nil {
		snapshot := engine.GetOrderBookSnapshot(20)
		s.tradeListener.OnOrderBookUpdate(snapshot)
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

		return nil
	})

	// 事務確定成功 (DB COMMIT 成功) 後，才去動記憶體
	if err == nil {
		if s.eventBus != nil {
			// Kafka 模式：以與 PlaceOrder 相同的 topic+partitionKey 發送撤單事件，
			// 確保 MatchingConsumer 在處理完所有同一 symbol 的掛單後，才執行 Cancel
			event := &OrderCancelRequestedEvent{
				EventType: EventOrderCancelRequested,
				Symbol:    orderPreCheck.Symbol,
				OrderID:   orderID,
				Side:      orderPreCheck.Side,
			}
			if err := s.eventBus.Publish(ctx, TopicOrders, orderPreCheck.Symbol, event); err != nil {
				log.Printf("發布 OrderCancelRequestedEvent 失敗: %v", err)
			}
		} else {
			// 無 Kafka Fallback：同步取消記憶體引擎中的訂單
			engine := s.engineManager.GetEngine(orderPreCheck.Symbol)
			var matchSide matching.OrderSide
			if orderPreCheck.Side == SideBuy {
				matchSide = matching.SideBuy
			} else {
				matchSide = matching.SideSell
			}
			engine.Cancel(orderID, matchSide)

			// 撤單後掛單簿已變更，推播最新深度
			if s.tradeListener != nil {
				snapshot := engine.GetOrderBookSnapshot(20)
				s.tradeListener.OnOrderBookUpdate(snapshot)
			}
		}
	}

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
