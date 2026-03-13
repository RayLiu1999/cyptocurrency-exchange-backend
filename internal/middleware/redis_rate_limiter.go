package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// 預先編譯 Lua Script 保證原子性，避免 TTL 被反覆重置的問題
var rateLimitScript = redisclient.NewScript(`
	local current = redis.call("INCR", KEYS[1])
	if current == 1 then
		redis.call("EXPIRE", KEYS[1], ARGV[1])
	end
	if current > tonumber(ARGV[2]) then
		return 0
	end
	return 1
`)

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
	// 加上 Timeout 防護，避免 Redis 網路異常卡死 API
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	key := fmt.Sprintf("exchange:ratelimit:%s", ip)

	// 執行 Lua Script
	res, err := rateLimitScript.Run(ctx, r.client.Client, []string{key}, int(r.window.Seconds()), r.limit).Int()
	if err != nil {
		// 若 Redis 異常，選擇 Fail-Open (放行請求)
		return true
	}

	return res == 1
}
