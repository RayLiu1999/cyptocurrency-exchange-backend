package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// idempotencyEntry 冪等性快取條目
type idempotencyEntry struct {
	StatusCode int       `json:"status_code"`
	Body       []byte    `json:"body"` // 儲存原始 JSON bytes
	ExpiresAt  time.Time `json:"expires_at"`
}

// IdempotencyStore 冪等性儲存介面（單體用 Memory，未來換 Redis SET NX）
type IdempotencyStore interface {
	// Get 取得已快取的結果，若不存在回傳 nil
	Get(key string) *idempotencyEntry
	// Set 儲存結果，ttl 後過期
	Set(key string, statusCode int, body []byte, ttl time.Duration)
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
	// 背景協程定期清理過期條目，避免 Memory 洩漏
	go s.cleanupLoop()
	return s
}

// Get 取得快取的回應（若已存在且未過期）
func (s *memoryIdempotencyStore) Get(key string) *idempotencyEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.store[key]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return nil
	}
	return entry
}

// Set 儲存回應至快取
// 複製一份 bytes 避免底層陣列被外部意外修改
func (s *memoryIdempotencyStore) Set(key string, statusCode int, body []byte, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bodyCopy := make([]byte, len(body))
	copy(bodyCopy, body)
	s.store[key] = &idempotencyEntry{
		StatusCode: statusCode,
		Body:       bodyCopy,
		ExpiresAt:  time.Now().Add(ttl),
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
			if now.After(entry.ExpiresAt) {
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

		// Cache Hit：直接用 c.Data 回傳儲存好的原始 JSON bytes，不觸發任何業務邏輯
		if entry := store.Get(key); entry != nil {
			c.Data(entry.StatusCode, "application/json", entry.Body)
			c.Abort()
			return
		}

		// Cache Miss：首次請求，用 bodyLogWriter 攔截 Handler 的回傳 bytes
		blw := &bodyLogWriter{ResponseWriter: c.Writer}
		c.Writer = blw
		c.Next()

		// Handler 完成後，若狀態 < 400 才快取（錯誤回應不應被重用）
		if blw.statusCode < 400 && len(blw.body) > 0 {
			store.Set(key, blw.statusCode, blw.body, ttl)
		}
	}
}

// bodyLogWriter 包裝 gin.ResponseWriter，同時攔截並記錄 Response bytes 以供快取
type bodyLogWriter struct {
	gin.ResponseWriter
	statusCode int
	body       []byte // 儲存 Handler 回傳的原始 bytes
}

// WriteHeader 攔截並記錄 HTTP Status Code
func (w *bodyLogWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Write 攔截 Response Body bytes：同時寫給 Client 並 append 到本地快取
// 修復：舊版只寫給 Client 但忘記存進 w.body，導致快取永遠是空 body
func (w *bodyLogWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	// 關鍵修復：將經過的 bytes append 至 w.body 以供後續快取
	w.body = append(w.body, b...)
	return w.ResponseWriter.Write(b)
}
