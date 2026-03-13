package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// 預先編譯 Lua Script 保證原子性
// 實作 Token Bucket (令牌桶) 演算法
var rateLimitScript = redisclient.NewScript(`
	local tokens_key = KEYS[1]
	local timestamp_key = KEYS[2]

	local rate = tonumber(ARGV[1])
	local capacity = tonumber(ARGV[2])
	local now = tonumber(ARGV[3])
	local requested = 1

	local fill_time = capacity / rate
	local ttl = math.floor(fill_time * 2)
	if ttl < 10 then
		ttl = 10
	end

	local last_tokens = tonumber(redis.call("get", tokens_key) or capacity)
	local last_refreshed = tonumber(redis.call("get", timestamp_key) or 0)

	local delta = math.max(0, now - last_refreshed)
	local filled_tokens = math.min(capacity, last_tokens + (delta * rate))

	local allowed = 0
	if filled_tokens >= requested then
		allowed = 1
		filled_tokens = filled_tokens - requested
	end

	redis.call("setex", tokens_key, ttl, filled_tokens)
	-- 更新時間為 now，使得下一次計算 delta 是基於這次請求的時間
	redis.call("setex", timestamp_key, ttl, now)

	return allowed
`)

// RedisRateLimiter 實作分散式 Redis 的限流器 (Token Bucket 演算法)
type RedisRateLimiter struct {
	client   *redis.Client
	rate     float64 // 每秒補充的令牌數
	capacity int     // 桶子最大容量 (Burst)
}

// NewRedisRateLimiter 建立一個 Redis Rate Limiter (相容原本簽章)
// 為了簡化，limit 與 window 會被轉換為對應的 rate 和 capacity (Burst 等於 limit)
func NewRedisRateLimiter(client *redis.Client, limit int, window time.Duration) RateLimiter {
	rate := float64(limit) / window.Seconds()
	return &RedisRateLimiter{
		client:   client,
		rate:     rate,
		capacity: limit,
	}
}

// Allow 判斷此 IP 的請求是否被允許
func (r *RedisRateLimiter) Allow(ip string) bool {
	// 加上 Timeout 防護，避免 Redis 網路異常卡死 API
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tokensKey := fmt.Sprintf("exchange:ratelimit:tokens:%s", ip)
	timestampKey := fmt.Sprintf("exchange:ratelimit:ts:%s", ip)

	// 取得精確到微秒的浮點數時間（秒為單位）
	now := float64(time.Now().UnixMicro()) / 1e6

	// 執行 Lua Script
	res, err := rateLimitScript.Run(ctx, r.client.Client, []string{tokensKey, timestampKey}, r.rate, r.capacity, now).Int()
	if err != nil {
		// 若 Redis 異常，選擇 Fail-Open (放行請求)
		return true
	}

	return res == 1
}
