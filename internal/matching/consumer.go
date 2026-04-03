package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"go.uber.org/zap"
)

type Service struct {
	engineManager *engine.EngineManager
	eventBus      domain.EventPublisher
	cacheRepo     domain.CacheRepository
}

func NewService(engineManager *engine.EngineManager, eventBus domain.EventPublisher, cacheRepo domain.CacheRepository) *Service {
	return &Service{
		engineManager: engineManager,
		eventBus:      eventBus,
		cacheRepo:     cacheRepo,
	}
}

// HandleEvents 是 Kafka exchange.orders Topic 的消費者 Handler。
// 撮合引擎訂閱此 Topic，依照 EventType 路由至對應的處理函式。
// 透過 symbol 作為 Partition Key，保證同一交易對的所有事件嚴格有序處理。
func (s *Service) HandleEvents(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("matching", "exchange.orders", err, time.Since(start))
	}()

	// 第一步：只解碼 EventType 決定路由，避免反覆完整解析
	var envelope struct {
		EventType domain.EventType `json:"event_type"`
	}
	if err = json.Unmarshal(value, &envelope); err != nil {
		return fmt.Errorf("解析 matching 事件失敗: %w", err)
	}

	switch envelope.EventType {
	case domain.EventOrderPlaced:
		var event domain.OrderPlacedEvent
		if err = json.Unmarshal(value, &event); err != nil {
			return fmt.Errorf("解析 OrderPlacedEvent 失敗: %w", err)
		}
		return s.handleOrderPlaced(ctx, &event)

	case domain.EventOrderCancelRequested:
		var event domain.OrderCancelRequestedEvent
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

func (s *Service) convertToMatchingOrder(event *domain.OrderPlacedEvent) *engine.Order {
	var matchSide engine.OrderSide
	if event.Side == domain.SideBuy {
		matchSide = engine.SideBuy
	} else {
		matchSide = engine.SideSell
	}

	if event.Type == domain.TypeLimit {
		return engine.NewOrder(event.OrderID, event.UserID, matchSide, event.Price, event.Quantity)
	} else {
		return engine.NewMarketOrder(event.OrderID, event.UserID, matchSide, event.Quantity)
	}
}

// handleOrderPlaced 接收 OrderPlacedEvent，執行記憶體撮合，並輸出結算事件。
func (s *Service) handleOrderPlaced(ctx context.Context, event *domain.OrderPlacedEvent) error {
	matchOrder := s.convertToMatchingOrder(event)
	eng := s.engineManager.GetEngine(event.Symbol)
	trades := eng.Process(matchOrder)

	// 判斷是否需要啟動結算：有成交、市價單、或 STP（剩餘量歸零）
	needsSettlement := len(trades) > 0 || event.Type == domain.TypeMarket || matchOrder.Quantity.IsZero()

	if needsSettlement && s.eventBus != nil {
		settlementEvent := &domain.SettlementRequestedEvent{
			EventType:      domain.EventSettlementRequested,
			Symbol:         event.Symbol,
			TakerOrderID:   event.OrderID,
			AmountLocked:   event.AmountLocked,
			LockedCurrency: event.LockedCurrency,
			RemainingQty:   matchOrder.Quantity, // 撮合後的剩餘數量（STP 偵測用）
			Trades:         trades,
		}
		// 🌟 修正：原地無限重試發布，絕不 return err 導致重新撮合
		for {
			err := s.eventBus.Publish(ctx, domain.TopicSettlements, event.Symbol, settlementEvent)
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
			tradeEvent := &domain.TradeExecutedEvent{
				EventType:    domain.EventTradeExecuted,
				TradeID:      trade.ID,
				Symbol:       event.Symbol,
				MakerOrderID: trade.MakerOrderID,
				TakerOrderID: trade.TakerOrderID,
				Price:        trade.Price,
				Quantity:     trade.Quantity,
			}
			// 原地無限重試發布行情事件
			for {
				err := s.eventBus.Publish(ctx, domain.TopicTrades, event.Symbol, tradeEvent)
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
	snapshot := eng.GetOrderBookSnapshot(20)
	s.OnOrderBookUpdate(event.Symbol, snapshot)

	return nil
}

// handleOrderCancelRequested 接收 OrderCancelRequestedEvent，從記憶體引擎移除掛單。
func (s *Service) handleOrderCancelRequested(ctx context.Context, event *domain.OrderCancelRequestedEvent) error {
	var matchSide engine.OrderSide
	if event.Side == domain.SideBuy {
		matchSide = engine.SideBuy
	} else {
		matchSide = engine.SideSell
	}

	eng := s.engineManager.GetEngine(event.Symbol)
	canceled := eng.Cancel(event.OrderID, matchSide)

	if canceled {
		if s.eventBus != nil {
			canceledEvent := &domain.OrderCanceledEvent{
				EventType: domain.EventOrderCanceled,
				Symbol:    event.Symbol,
				OrderID:   event.OrderID,
				UserID:    event.UserID,
			}
			for {
				err := s.eventBus.Publish(ctx, domain.TopicSettlements, event.Symbol, canceledEvent)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				logger.Warn("發布 OrderCanceledEvent 失敗，1秒後重試",
					zap.String("order_id", event.OrderID.String()),
					zap.Error(err),
				)
				time.Sleep(1 * time.Second)
			}
		}

		// 掛單簿已變更，更新快取並推播 WebSocket
		snapshot := eng.GetOrderBookSnapshot(20)
		s.OnOrderBookUpdate(event.Symbol, snapshot)
	}

	return nil
}

// OnOrderBookUpdate 收到快照後更新 Redis 快取，並發布更新事件
func (s *Service) OnOrderBookUpdate(symbol string, snapshot *engine.OrderBookSnapshot) {
	// 1. 更新 Redis 快取
	if s.cacheRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.cacheRepo.SetOrderBookSnapshot(ctx, snapshot); err != nil {
			logger.Warn("更新 Redis OrderBook 失敗", zap.Error(err))
		}
	}

	// 2. 發行 Kafka 更新事件以供 websocket 推播
	if s.eventBus != nil {
		event := &domain.OrderBookUpdatedEvent{
			EventType: domain.EventOrderBookUpdated,
			Symbol:    symbol,
			Snapshot:  snapshot,
		}
		// 推播給市場資料服務不 block 核心引擎
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		if err := s.eventBus.Publish(ctx, domain.TopicOrderBook, symbol, event); err != nil {
			logger.Warn("發布 OrderBookUpdatedEvent 失敗", zap.Error(err))
		}
	}
}
