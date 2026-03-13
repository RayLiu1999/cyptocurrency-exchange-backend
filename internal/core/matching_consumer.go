package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/RayLiu1999/exchange/internal/core/matching"
)

// HandleMatchingEvent 是 Kafka exchange.orders Topic 的消費者 Handler。
// 撮合引擎訂閱此 Topic，依照 EventType 路由至對應的處理函式。
// 透過 symbol 作為 Partition Key，保證同一交易對的所有事件嚴格有序處理。
func (s *ExchangeServiceImpl) HandleMatchingEvent(ctx context.Context, key, value []byte) error {
	// 第一步：只解碼 EventType 決定路由，避免反覆完整解析
	var envelope struct {
		EventType EventType `json:"event_type"`
	}
	if err := json.Unmarshal(value, &envelope); err != nil {
		return fmt.Errorf("解析 matching 事件失敗: %w", err)
	}

	switch envelope.EventType {
	case EventOrderPlaced:
		var event OrderPlacedEvent
		if err := json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 OrderPlacedEvent 失敗: %w", err)
		}
		return s.handleOrderPlaced(ctx, &event)

	case EventOrderCancelRequested:
		var event OrderCancelRequestedEvent
		if err := json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 OrderCancelRequestedEvent 失敗: %w", err)
		}
		return s.handleOrderCancelRequested(ctx, &event)

	default:
		// 未知事件：記錄警告後 Commit（避免 Consumer 卡在同一筆訊息）
		log.Printf("⚠️  HandleMatchingEvent 收到未知 EventType: %s，跳過", envelope.EventType)
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
			TakerOrderID:   event.OrderID,
			AmountLocked:   event.AmountLocked,
			LockedCurrency: event.LockedCurrency,
			RemainingQty:   matchOrder.Quantity, // 撮合後的剩餘數量（STP 偵測用）
			Trades:         trades,
		}
		if err := s.eventBus.Publish(ctx, TopicSettlements, event.Symbol, settlementEvent); err != nil {
			// 結算事件發布失敗是嚴重錯誤；返回 error 讓 Consumer 重試（at-least-once）
			return fmt.Errorf("發布 SettlementRequestedEvent 失敗: %w", err)
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
			if err := s.eventBus.Publish(ctx, TopicTrades, event.Symbol, tradeEvent); err != nil {
				log.Printf("發布 TradeExecutedEvent 失敗 (TradeID: %s): %v", trade.ID, err)
				// 不返回 error：行情事件失敗不應阻塞結算流程
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
