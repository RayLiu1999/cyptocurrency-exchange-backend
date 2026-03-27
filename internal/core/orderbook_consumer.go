package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
)

// HandleOrderBookEvent 是 Kafka exchange.orderbook Topic 的消費者 Handler。
// 僅供微服務模式下的 order-service 使用。
// matching-engine 撮合完成後，透過 Kafka 推播掛單簿快照，此 handler 接收後轉發至 WebSocket。
func (s *ExchangeServiceImpl) HandleOrderBookEvent(ctx context.Context, key, value []byte) (err error) {
	start := time.Now()
	defer func() {
		metrics.ObserveKafkaEvent("market-data", "exchange.orderbook", err, time.Since(start))
	}()

	var event OrderBookUpdatedEvent
	if err = json.Unmarshal(value, &event); err != nil {
		return fmt.Errorf("解析 OrderBookUpdatedEvent 失敗: %w", err)
	}

	if event.Snapshot == nil {
		return nil
	}

	// 此時 order-service 的 tradeListener 已設定（wsHandler），
	// OnOrderBookUpdate 會觸發 WebSocket 推播（而非再次發布 Kafka，避免無限循環）
	s.OnOrderBookUpdate(event.Snapshot)
	return nil
}
