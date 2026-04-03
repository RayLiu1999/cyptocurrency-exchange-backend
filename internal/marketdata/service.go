package marketdata

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

type TradeEventListener interface {
	OnTrade(trade *engine.Trade)
	OnOrderUpdate(order *domain.Order)
	OnOrderBookUpdate(snapshot *engine.OrderBookSnapshot)
}

// Service 是 market-data-service 專屬的輕量服務。
// 職責：接收來自 Kafka 的行情事件，透過 TradeEventListener 推播至 WebSocket 連線。
// 刻意不依賴 ExchangeServiceImpl，以維持清晰的微服務邊界。
type Service struct {
	tradeListener TradeEventListener
	cacheRepo     domain.CacheRepository
}

// NewService 建立 market-data-service 的核心服務
func NewService(tradeListener TradeEventListener, cacheRepo domain.CacheRepository) *Service {
	return &Service{
		tradeListener: tradeListener,
		cacheRepo:     cacheRepo,
	}
}

// HandleOrderBook 是 Kafka exchange.orderbook Topic 的消費者 Handler。
// matching-engine 撮合完成後，透過 Kafka 推播掛單簿快照，此 handler 接收後轉發至 WebSocket。
func (s *Service) HandleOrderBook(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("market-data", "exchange.orderbook", err, time.Since(start))
	}()

	var event domain.OrderBookUpdatedEvent
	if err = json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 OrderBookUpdatedEvent 失敗: %w", err)
	}

	if event.Snapshot == nil {
		return nil
	}

	// 1. 更新 Redis 快取（供 HTTP API 查詢）
	if s.cacheRepo != nil {
		if err := s.cacheRepo.SetOrderBookSnapshot(ctx, event.Snapshot); err != nil {
			logger.Log.Error("更新 OrderBook 快取失敗", zap.Error(err), zap.String("symbol", event.Snapshot.Symbol))
		}
	}

	// 2. 推播至 WebSocket
	if s.tradeListener != nil {
		s.tradeListener.OnOrderBookUpdate(event.Snapshot)
	}
	return nil
}

// HandleTrade 是 Kafka exchange.trades Topic 的消費者 Handler。
func (s *Service) HandleTrade(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("market-data", "exchange.trades", err, time.Since(start))
	}()

	var event domain.TradeExecutedEvent
	if err = json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 TradeExecutedEvent 失敗: %w", err)
	}

	if s.tradeListener == nil {
		return nil
	}

	s.tradeListener.OnTrade(&engine.Trade{
		ID:           event.TradeID,
		Symbol:       event.Symbol,
		MakerOrderID: event.MakerOrderID,
		TakerOrderID: event.TakerOrderID,
		Price:        event.Price,
		Quantity:     event.Quantity,
	})
	return nil
}

// HandleOrderUpdated 是 Kafka exchange.order_updates Topic 的消費者 Handler。
func (s *Service) HandleOrderUpdated(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("market-data", "exchange.order_updates", err, time.Since(start))
	}()

	var event domain.OrderUpdatedEvent
	if err = json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 OrderUpdatedEvent 失敗: %w", err)
	}

	if s.tradeListener == nil || event.Order == nil {
		return nil
	}

	s.tradeListener.OnOrderUpdate(event.Order)
	return nil
}
