package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiter 限流器介面（單體用 Memory，未來換 Redis Adapter）
type RateLimiter interface {
	Allow(key string) bool
}

// memoryRateLimiter 基於 In-Memory 的 Token Bucket 限流器
type memoryRateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.Mutex
	r        rate.Limit // 每秒允許的請求速率
	b        int        // Bucket 最大容量（即一次性可爆發的請求數）
	ttl      time.Duration
	accessed map[string]time.Time // 記錄最後存取時間，用於清理過期的 limiter
}

// NewMemoryRateLimiter 建立基於記憶體的 Token Bucket 限流器
// r: 每秒允許速率, b: 爆發容量（通常等於 r）, ttl: 閒置後多久清理
func NewMemoryRateLimiter(r rate.Limit, b int, ttl time.Duration) RateLimiter {
	rl := &memoryRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		accessed: make(map[string]time.Time),
		r:        r,
		b:        b,
		ttl:      ttl,
	}
	// 背景協程定期清理長時間不活躍的 limiter，避免 Memory 洩漏
	go rl.cleanupLoop()
	return rl
}

// Allow 嘗試消耗一個令牌，回傳是否允許通過
func (m *memoryRateLimiter) Allow(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.limiters[key]; !exists {
		m.limiters[key] = rate.NewLimiter(m.r, m.b)
	}
	m.accessed[key] = time.Now()
	return m.limiters[key].Allow()
}

// cleanupLoop 每分鐘清理一次長時間未使用的 limiter，防止記憶體無限成長
func (m *memoryRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for key, lastSeen := range m.accessed {
			if now.Sub(lastSeen) > m.ttl {
				delete(m.limiters, key)
				delete(m.accessed, key)
			}
		}
		m.mu.Unlock()
	}
}

// RateLimitMiddleware 建立限流 Gin Middleware
func RateLimitMiddleware(limiter RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 以 IP 作為限流 key（未來有了 JWT 可換成 UserID）
		ip := c.ClientIP()
		if !limiter.Allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "請求過於頻繁，請稍後再試 (Rate limit exceeded)",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
