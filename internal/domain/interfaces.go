package domain

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/matching/engine"
)

// CacheRepository 定義快取儲存層介面
type CacheRepository interface {
	GetOrderBookSnapshot(ctx context.Context, symbol string) (*engine.OrderBookSnapshot, error)
	SetOrderBookSnapshot(ctx context.Context, snapshot *engine.OrderBookSnapshot) error
}

// EventPublisher 定義事件發布介面
type EventPublisher interface {
	Publish(ctx context.Context, topic, key string, payload interface{}) error
	Close()
}
