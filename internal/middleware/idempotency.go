package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// idempotencyEntry 冪等性快取條目
type idempotencyEntry struct {
	statusCode int
	body       gin.H
	expiresAt  time.Time
}

// IdempotencyStore 冪等性儲存介面（單體用 Memory，未來換 Redis SET NX）
type IdempotencyStore interface {
	// Get 取得已快取的結果，若不存在回傳 nil
	Get(key string) *idempotencyEntry
	// Set 儲存結果，ttl 後過期
	Set(key string, statusCode int, body gin.H, ttl time.Duration)
}

// memoryIdempotencyStore 基於 In-Memory 的冪等性儲存
type memoryIdempotencyStore struct {
	mu    sync.RWMutex
	store map[string]*idempotencyEntry
}

// NewMemoryIdempotencyStore 建立 Memory 冪等性儲存
func NewMemoryIdempotencyStore() IdempotencyStore {
	s := &memoryIdempotencyStore{
		store: make(map[string]*idempotencyEntry),
	}
	// 背景協程定期清理過期條目
	go s.cleanupLoop()
	return s
}

// Get 取得快取的回應（若已存在且未過期）
func (s *memoryIdempotencyStore) Get(key string) *idempotencyEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.store[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry
}

// Set 儲存回應至快取
func (s *memoryIdempotencyStore) Set(key string, statusCode int, body gin.H, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[key] = &idempotencyEntry{
		statusCode: statusCode,
		body:       body,
		expiresAt:  time.Now().Add(ttl),
	}
}

// cleanupLoop 定期清理過期的冪等性記錄
func (s *memoryIdempotencyStore) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for key, entry := range s.store {
			if now.After(entry.expiresAt) {
				delete(s.store, key)
			}
		}
		s.mu.Unlock()
	}
}

// IdempotencyMiddleware 建立冪等性 Gin Middleware
// Client 必須在 Header 帶 Idempotency-Key，若同一個 Key 已被處理，直接回傳快取結果。
func IdempotencyMiddleware(store IdempotencyStore, ttl time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			// 若 Client 沒帶 Key，視為不需要冪等性保護，直接放行
			c.Next()
			return
		}

		// Cache Hit：此 Key 已被處理過，直接回傳快取結果，不觸發任何業務邏輯
		if entry := store.Get(key); entry != nil {
			c.JSON(entry.statusCode, entry.body)
			c.Abort()
			return
		}

		// Cache Miss：首次請求，正常執行，並在完成後將結果快取
		// 透過替換 ResponseWriter 的方式攔截 Handler 的回傳值
		blw := &bodyLogWriter{body: gin.H{}, ResponseWriter: c.Writer}
		c.Writer = blw
		c.Next()

		// Handler 完成後，若狀態 >= 400 則不快取（錯誤回應不應被重用）
		if blw.statusCode < 400 {
			store.Set(key, blw.statusCode, blw.body, ttl)
		}
	}
}

// bodyLogWriter 包裝 gin.ResponseWriter，攔截 JSON 回應內容以供快取
type bodyLogWriter struct {
	gin.ResponseWriter
	statusCode int
	body       gin.H
}

// WriteHeader 攔截並記錄 HTTP Status Code
func (w *bodyLogWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// WriteJSON 攔截 JSON 回傳（Gin 的 c.JSON 最終會呼叫此方法）
func (w *bodyLogWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}
