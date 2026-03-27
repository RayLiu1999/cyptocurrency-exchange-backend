package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"go.uber.org/zap"
)

// HandleMatchingEvent 是 Kafka exchange.orders Topic 的消費者 Handler。
// 撮合引擎訂閱此 Topic，依照 EventType 路由至對應的處理函式。
// 透過 symbol 作為 Partition Key，保證同一交易對的所有事件嚴格有序處理。
func (s *ExchangeServiceImpl) HandleMatchingEvent(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("matching", "exchange.orders", err, time.Since(start))
	}()

	// 第一步：只解碼 EventType 決定路由，避免反覆完整解析
	var envelope struct {
		EventType EventType `json:"event_type"`
	}
	if err = json.Unmarshal(value, &envelope); err != nil {
		return fmt.Errorf("解析 matching 事件失敗: %w", err)
	}

	switch envelope.EventType {
	case EventOrderPlaced:
		var event OrderPlacedEvent
		if err = json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 OrderPlacedEvent 失敗: %w", err)
		}
		return s.handleOrderPlaced(ctx, &event)

	case EventOrderCancelRequested:
		var event OrderCancelRequestedEvent
		if err = json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 OrderCancelRequestedEvent 失敗: %w", err)
		}
		return s.handleOrderCancelRequested(ctx, &event)

	default:
		// 未知事件：記錄警告後 Commit（避免 Consumer 卡在同一筆訊息）
		logger.Warn("HandleMatchingEvent 收到未知 EventType，跳過",
			zap.String("event_type", string(envelope.EventType)),
		)
		return nil
	}
}

// handleOrderPlaced 接收 OrderPlacedEvent，執行記憶體撮合，並輸出結算事件。
func (s *ExchangeServiceImpl) handleOrderPlaced(ctx context.Context, event *OrderPlacedEvent) error {
	// 從事件重建 Order 物件（僅含撮合引擎所需欄位）
	order := &Order{
		ID:       event.OrderID,
		UserID:   event.UserID,
		Symbol:   event.Symbol,
		Side:     event.Side,
		Type:     event.Type,
		Price:    event.Price,
		Quantity: event.Quantity,
	}

	matchOrder := s.convertToMatchingOrder(order)
	engine := s.engineManager.GetEngine(event.Symbol)
	trades := engine.Process(matchOrder)

	// 判斷是否需要啟動結算：有成交、市價單、或 STP（剩餘量歸零）
	needsSettlement := len(trades) > 0 || order.Type == TypeMarket || matchOrder.Quantity.IsZero()

	if needsSettlement && s.eventBus != nil {
		settlementEvent := &SettlementRequestedEvent{
			EventType:      EventSettlementRequested,
			Symbol:         event.Symbol,
			TakerOrderID:   event.OrderID,
			AmountLocked:   event.AmountLocked,
			LockedCurrency: event.LockedCurrency,
			RemainingQty:   matchOrder.Quantity, // 撮合後的剩餘數量（STP 偵測用）
			Trades:         trades,
		}
		// 🌟 修正：原地無限重試發布，絕不 return err 導致重新撮合
		for {
			err := s.eventBus.Publish(ctx, TopicSettlements, event.Symbol, settlementEvent)
			if err == nil {
				break // 發布成功，繼續往下走
			}

			// 如果是優雅關機觸發的 Context 取消，我們直接 return
			// 這樣外層 Consumer 就不會 Commit，重啟後引擎會從 DB Hydration 恢復，完全安全！
			if ctx.Err() != nil {
				return ctx.Err()
			}

			logger.Warn("發布 SettlementRequestedEvent 失敗，1秒後重試",
				zap.String("symbol", event.Symbol),
				zap.Error(err),
			)
			time.Sleep(1 * time.Second)
		}

		// 個別 TradeExecutedEvent 供外部系統訂閱（例如：行情推播、交易歷史）
		for _, trade := range trades {
			tradeEvent := &TradeExecutedEvent{
				EventType:    EventTradeExecuted,
				TradeID:      trade.ID,
				Symbol:       event.Symbol,
				MakerOrderID: trade.MakerOrderID,
				TakerOrderID: trade.TakerOrderID,
				Price:        trade.Price,
				Quantity:     trade.Quantity,
			}
			// 原地無限重試發布行情事件
			for {
				err := s.eventBus.Publish(ctx, TopicTrades, event.Symbol, tradeEvent)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				logger.Warn("發布 TradeExecutedEvent 失敗，1秒後重試",
					zap.String("trade_id", trade.ID.String()),
					zap.Error(err),
				)
				time.Sleep(1 * time.Second)
			}
		}
	}

	// 無論是否有成交，掛單簿已變更（新增、吃單），同步更新 Redis 快取並推播 WebSocket
	snapshot := engine.GetOrderBookSnapshot(20)
	s.OnOrderBookUpdate(snapshot)

	return nil
}

// handleOrderCancelRequested 接收 OrderCancelRequestedEvent，從記憶體引擎移除掛單。
func (s *ExchangeServiceImpl) handleOrderCancelRequested(ctx context.Context, event *OrderCancelRequestedEvent) error {
	var matchSide matching.OrderSide
	if event.Side == SideBuy {
		matchSide = matching.SideBuy
	} else {
		matchSide = matching.SideSell
	}

	engine := s.engineManager.GetEngine(event.Symbol)
	engine.Cancel(event.OrderID, matchSide)

	// 掛單簿已變更，更新快取並推播 WebSocket
	snapshot := engine.GetOrderBookSnapshot(20)
	s.OnOrderBookUpdate(snapshot)

	return nil
}
