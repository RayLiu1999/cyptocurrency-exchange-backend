package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
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

// SetOrderBookSnapshot 將訂單簿快照寫入 Redis，TTL 設為 10 秒
// 為什麼只設 10 秒？因為在高頻交易下，訂單簿頻繁更新，10秒內一定有下一個覆蓋。
// 若 10 秒無交易，讓快取自然過期，下次讀取時 fallback 回 Memory 也能確保資料新鮮。
func (r *RedisCacheRepository) SetOrderBookSnapshot(ctx context.Context, snapshot *engine.OrderBookSnapshot) error {
	key := fmt.Sprintf("exchange:orderbook:%s", snapshot.Symbol)

	// 序列化成 JSON
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("序列化快取失敗: %w", err)
	}

	// 寫入 Redis 並設定 TTL
	if err := r.client.Client.Set(ctx, key, data, 10*time.Second).Err(); err != nil {
		return fmt.Errorf("寫入 Redis 失敗: %w", err)
	}

	return nil
}
