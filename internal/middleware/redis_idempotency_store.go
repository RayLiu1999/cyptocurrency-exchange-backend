package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"go.uber.org/zap"
)

// RedisIdempotencyStore 實作基於 Redis 的冪等性儲存
type RedisIdempotencyStore struct {
	client *redis.Client
}

// NewRedisIdempotencyStore 建立一個 Redis 冪等性儲存實例
func NewRedisIdempotencyStore(client *redis.Client) IdempotencyStore {
	return &RedisIdempotencyStore{
		client: client,
	}
}

// Get 取得已快取的冪等性結果
func (s *RedisIdempotencyStore) Get(key string) *idempotencyEntry {
	// 加上 2 秒超時防護
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	redisKey := fmt.Sprintf("exchange:idempotency:%s", key)

	val, err := s.client.Client.Get(ctx, redisKey).Bytes()
	if err != nil {
		// redis.Nil 代表 Cache Miss
		return nil
	}

	// 反序列化 Redis 中的 JSON 至 idempotencyEntry
	// 由於 entry 的 body 也是 []byte，JSON.Unmarshal 會轉成 base64 字串解碼
	var entry idempotencyEntry
	if err := json.Unmarshal(val, &entry); err != nil {
		logger.Error("反序列化 Redis 冪等性快取失敗", zap.Error(err), zap.String("key", key))
		return nil
	}

	return &entry
}

// Set 寫入冪等性結果（帶有 TTL）
func (s *RedisIdempotencyStore) Set(key string, statusCode int, body []byte, ttl time.Duration) {
	// 加上 2 秒超時防護
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	redisKey := fmt.Sprintf("exchange:idempotency:%s", key)

	entry := idempotencyEntry{
		StatusCode: statusCode,
		Body:       body,
		ExpiresAt:  time.Now().Add(ttl),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		logger.Error("序列化冪等性紀錄失敗", zap.Error(err))
		return
	}

	// 寫入 Redis
	if err := s.client.Client.Set(ctx, redisKey, data, ttl).Err(); err != nil {
		logger.Error("寫入 Redis 冪等性快取失敗", zap.Error(err), zap.String("key", key))
	}
}
