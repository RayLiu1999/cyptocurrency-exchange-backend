package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
)

// RedisCacheRepository 實作 core.CacheRepository 介面 (Redis 版本)
type RedisCacheRepository struct {
	client *redis.Client
}

// NewRedisCacheRepository 建立 Redis 快取實作
func NewRedisCacheRepository(client *redis.Client) domain.CacheRepository {
	return &RedisCacheRepository{
		client: client,
	}
}

// GetOrderBookSnapshot 從 Redis 讀取指定交易對的訂單簿快照
func (r *RedisCacheRepository) GetOrderBookSnapshot(ctx context.Context, symbol string) (*engine.OrderBookSnapshot, error) {
	key := fmt.Sprintf("exchange:orderbook:%s", symbol)

	// 從 Redis 取得 JSON 字串
	data, err := r.client.Client.Get(ctx, key).Bytes()
	if err != nil {
		// redis.Nil 代表 Key 不存在 (Cache Miss)
		return nil, err
	}

	// 反序列化 JSON 回 OrderBookSnapshot
	var snapshot engine.OrderBookSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("反序列化快取失敗: %w", err)
	}

	return &snapshot, nil
}

// SetOrderBookSnapshot 將訂單簿快照寫入 Redis。
// 這裡刻意不設定 TTL，因為 market-data-service 的讀路徑以 Redis 為唯一快取來源；
// 若靜態掛單簿在無交易期間過期，對外查詢會誤判成空盤。
func (r *RedisCacheRepository) SetOrderBookSnapshot(ctx context.Context, snapshot *engine.OrderBookSnapshot) error {
	key := fmt.Sprintf("exchange:orderbook:%s", snapshot.Symbol)

	// 序列化成 JSON
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("序列化快取失敗: %w", err)
	}

	// 寫入 Redis，保持到下一次快照覆蓋。
	if err := r.client.Client.Set(ctx, key, data, 0).Err(); err != nil {
		return fmt.Errorf("寫入 Redis 失敗: %w", err)
	}

	return nil
}
