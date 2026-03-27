package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
)

// HandleTradeEvent 是 Kafka exchange.trades Topic 的消費者 Handler。
// market-data-service 會將成交事件轉成 WebSocket 推播。
func (s *ExchangeServiceImpl) HandleTradeEvent(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("market-data", "exchange.trades", err, time.Since(start))
	}()

	var event TradeExecutedEvent
	if err = json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 TradeExecutedEvent 失敗: %w", err)
	}

	if s.tradeListener == nil {
		return nil
	}

	s.tradeListener.OnTrade(&matching.Trade{
		ID:           event.TradeID,
		Symbol:       event.Symbol,
		MakerOrderID: event.MakerOrderID,
		TakerOrderID: event.TakerOrderID,
		Price:        event.Price,
		Quantity:     event.Quantity,
	})
	return nil
}

// HandleOrderUpdatedEvent 是 Kafka exchange.order_updates Topic 的消費者 Handler。
// market-data-service 會將訂單狀態更新轉成 WebSocket 推播。
func (s *ExchangeServiceImpl) HandleOrderUpdatedEvent(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("market-data", "exchange.order_updates", err, time.Since(start))
	}()

	var event OrderUpdatedEvent
	if err = json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 OrderUpdatedEvent 失敗: %w", err)
	}

	if s.tradeListener == nil || event.Order == nil {
		return nil
	}

	s.tradeListener.OnOrderUpdate(event.Order)
	return nil
}
