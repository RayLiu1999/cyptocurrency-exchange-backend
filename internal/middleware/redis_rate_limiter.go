package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
)

// RedisRateLimiter 實作分散式 Redis 的限流器 (Fixed Window 演算法)
type RedisRateLimiter struct {
	client *redis.Client
	limit  int           // 限制的請求次數
	window time.Duration // 固定視窗時間
}

// NewRedisRateLimiter 建立一個 Redis Rate Limiter
func NewRedisRateLimiter(client *redis.Client, limit int, window time.Duration) RateLimiter {
	return &RedisRateLimiter{
		client: client,
		limit:  limit,
		window: window,
	}
}

// Allow 判斷此 IP 的請求是否被允許
func (r *RedisRateLimiter) Allow(ip string) bool {
	ctx := context.Background()
	key := fmt.Sprintf("exchange:ratelimit:%s", ip)

	// 使用 Pipeline 來保證原子性並減少網路來回
	pipe := r.client.Client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, r.window) // 覆蓋 TTL 為視窗時間

	_, err := pipe.Exec(ctx)
	if err != nil {
		// 若 Redis 異常，選擇 Fail-Open (放行請求)
		// 避免 Redis 當機導致首頁進不去，可自行依據業務重要性改為 Fail-Close
		return true
	}

	count := incr.Val()
	return count <= int64(r.limit)
}
