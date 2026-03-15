# 基礎設施解析 (6)：Redis 在交易所中的三大戰場

> **本文摘要**：Redis 在本系統中不只是「快取」，它同時承擔了三個完全不同的角色：
> 1. **訂單簿快取**：減少 WebSocket 查詢的讀取壓力
> 2. **分散式限流器**：用 Lua 腳本保證跨節點的 Token Bucket 原子性
> 3. **API 冪等性儲存**：防止網路重試導致的重複下單

---

## 戰場一：訂單簿快取 (Order Book Snapshot Cache)

### 核心問題：掛單簿深度查詢的讀放大

WebSocket 每次廣播要推送掛單簿快照。如果每次都去掃 PostgreSQL 然後重新組裝，在高頻交易下（每秒數百次撮合），這會讓資料庫不堪重負。

```
❌ 沒有 Redis 的流程：
每次 WebSocket 請求 → SELECT * FROM orders WHERE status = NEW → 組裝 → 推播
(n 個連線 × 每秒 m 次 = n*m 次 DB 查詢)

✅ 有 Redis 的流程：
撮合完成後 → 主動 SET 快照進 Redis (TTL=10s)
每次 WebSocket 請求 → GET snapshot FROM Redis (μs 級回應)
```

### 介面設計 (Dependency Inversion)

Core 層完全不知道 Redis 的存在，只依賴介面：

```go
// internal/core/ports.go
// CacheRepository 定義快取儲存層介面 (依賴反轉)
type CacheRepository interface {
    GetOrderBookSnapshot(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error)
    SetOrderBookSnapshot(ctx context.Context, snapshot *matching.OrderBookSnapshot) error
}
```

Redis 實作在基礎設施層：

```go
// internal/repository/redis_cache_repository.go
func (r *RedisCacheRepository) SetOrderBookSnapshot(ctx context.Context, snapshot *matching.OrderBookSnapshot) error {
    key := fmt.Sprintf("exchange:orderbook:%s", snapshot.Symbol) // 例如 "exchange:orderbook:BTC-USD"

    data, err := json.Marshal(snapshot)
    if err != nil { ... }

    // TTL 設為 10 秒的設計考量：
    // 高頻交易下，10 秒內必有新撮合覆蓋，不需要更長。
    // 若 10 秒無任何交易（冷門交易對），讓快取自然過期，
    // 下次讀取 fallback 回 Memory Engine，確保資料絕對新鮮。
    return r.client.Client.Set(ctx, key, data, 10*time.Second).Err()
}
```

### Cache Miss 降級策略

```go
// internal/core/market_service.go (GetOrderBook)
func (s *ExchangeServiceImpl) GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error) {
    // 1. 嘗試從 Redis 讀 (Cache Hit: μs 級)
    if s.cacheRepo != nil {
        snapshot, err := s.cacheRepo.GetOrderBookSnapshot(ctx, symbol)
        if err == nil {
            return snapshot, nil // ✅ Cache Hit
        }
        // redis.Nil 代表 key 不存在 (Cache Miss)，繼續走降級
    }

    // 2. Cache Miss 或 Redis 不可用：從 In-Memory 引擎讀取
    engine := s.engineManager.GetEngine(symbol)
    snapshot := engine.GetOrderBookSnapshot(20) // 前 20 檔深度
    return snapshot, nil
}
```

> **設計亮點**：即使 Redis **完全宕機**，系統也能透過 In-Memory Engine 正常運作。這是一個 Graceful Degradation（優雅降級）範例。

---

## 戰場二：分散式限流器 (Distributed Rate Limiter)

### 核心問題：多節點部署時，記憶體限流器「各自為政」

```
❌ 單機 In-Memory Token Bucket 的問題：
                  使用者
               ↙    ↘
       Node A       Node B
       60次/分      60次/分
       → 實際等同於允許 120次/分！

✅ Redis 分散式 Token Bucket：
                  使用者
               ↙    ↘
       Node A       Node B
          ↓             ↓
        Redis（共享 Token 狀態）
        → 真正的 60次/分，跨節點全局有效
```

### 為什麼要用 Lua Script？

Token Bucket 演算法的核心是「先讀 → 計算 → 寫回」，這是一個典型的 Read-Modify-Write，在高並發下會有 Race Condition：

```
❌ 不安全的寫法（偽代碼）：
GET tokens            // Node A 和 Node B 同時讀到 tokens = 1
計算 filled_tokens = 2
SET tokens = 1        // 兩者都消耗了 1 個，但最後都存回 1，等於沒扣！

✅ Lua Script 在 Redis 中原子執行：
-- 整個操作在 Redis 服務端是「原子的」，不可分割
local last_tokens = tonumber(redis.call("get", tokens_key) or capacity)
local delta = math.max(0, now - last_refreshed)
local filled_tokens = math.min(capacity, last_tokens + (delta * rate))
-- ... 計算並原子地寫回
```

### 關鍵實作細節

```go
// internal/middleware/redis_rate_limiter.go

// 用 Go 預先編譯腳本（避免每次請求都重新解析）
var rateLimitScript = redisclient.NewScript(`
    local tokens_key = KEYS[1]
    local timestamp_key = KEYS[2]
    local rate = tonumber(ARGV[1])   -- 每秒補充速率
    local capacity = tonumber(ARGV[2]) -- 桶子容量
    local now = tonumber(ARGV[3])    -- 精確到微秒的時間戳

    -- 從 Redis 讀取上次狀態
    local last_tokens = tonumber(redis.call("get", tokens_key) or capacity)
    local last_refreshed = tonumber(redis.call("get", timestamp_key) or 0)

    -- 計算時間差，補充令牌
    local delta = math.max(0, now - last_refreshed)
    local filled_tokens = math.min(capacity, last_tokens + (delta * rate))

    -- 判斷是否允許
    local allowed = 0
    if filled_tokens >= 1 then
        allowed = 1
        filled_tokens = filled_tokens - 1
    end

    redis.call("setex", tokens_key, ttl, filled_tokens)
    redis.call("setex", timestamp_key, ttl, now)
    return allowed
`)

func (r *RedisRateLimiter) Allow(ip string) bool {
    // 每個 IP 有獨立的 Key，避免互相干擾
    tokensKey := fmt.Sprintf("exchange:ratelimit:tokens:%s", ip)
    timestampKey := fmt.Sprintf("exchange:ratelimit:ts:%s", ip)

    // 精確到微秒確保時間差計算準確
    now := float64(time.Now().UnixMicro()) / 1e6

    res, err := rateLimitScript.Run(ctx, r.client.Client, []string{tokensKey, timestampKey},
        r.rate, r.capacity, now).Int()

    if err != nil {
        // Fail-Open 策略：Redis 異常時放行請求，避免 Redis 故障連累主業務
        return true
    }
    return res == 1
}
```

### 單機 vs 分散式：雙軌並行

系統在啟動時根據 Redis 是否可用，自動選擇限流後端：

```go
// cmd/server/main.go
if redisClient != nil {
    // Redis 可用：使用分散式限流（跨節點有效）
    publicLimiter = middleware.NewRedisRateLimiter(redisClient, 60, time.Minute)
    privateLimiter = middleware.NewRedisRateLimiter(redisClient, 10, 1*time.Second)
} else {
    // Redis 不可用：降級到單機 In-Memory Token Bucket
    publicLimiter = middleware.NewMemoryRateLimiter(1, 60, 10*time.Minute)
    privateLimiter = middleware.NewMemoryRateLimiter(10, 10, 10*time.Minute)
}
```

---

## 戰場三：API 冪等性儲存 (Idempotency Store)

### 核心問題：網路重試導致重複下單

```
❌ 無冪等性保護的場景：
1. 前端發出「買 1 BTC」的請求 (Idempotency-Key: abc-123)
2. 網路閃斷，前端沒收到 200 OK
3. 前端自動重試，又發了一次「買 1 BTC」
4. 結果：使用者買了 2 BTC！💸

✅ 有冪等性保護後：
第一次請求 → 處理 → 存入 Redis {key: "abc-123", response: {...}}
第二次重試 → 在 Redis 找到 "abc-123" → 直接回傳之前的 response → 不再執行任何業務邏輯
```

### 冪等性的 HTTP 協議設計

```
POST /api/v1/orders
Headers:
    Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
Body:
    {"symbol": "BTC-USD", "side": "BUY", "type": "LIMIT", ...}
```

前端為每次「可能被重試」的請求生成一個唯一 UUID 作為 `Idempotency-Key`。

### 流程設計

```go
// Middleware 攔截 → 查 Redis → 決定是否繼續執行

// 1. 查 Redis (嘗試擷取緩存回應)
existing := store.Get(key)
if existing != nil && existing.Completed {
    // Cache Hit：直接回傳之前成功的結果
    c.JSON(existing.Status, existing.Body)
    c.Abort()
    return
}

// 2. Cache Miss：繼續執行 Handler，完成後存入 Redis
// 3. 設定 24 小時 TTL
store.Set(key, &idempotencyEntry{
    Status:    responseStatus,
    Body:      responseBody,
    Completed: true,
}, 24*time.Hour)
```

### Redis Key 設計

```
exchange:idempotency:{idempotency-key}
→ Value: JSON { "status": 202, "body": {...}, "completed": true }
→ TTL: 24 小時（行業標準：足夠長讓所有重試結束，又不佔用太多記憶體）
```

---

## Redis 整體架構圖

```
                         ┌──────────────────────────────────────┐
                         │               Redis                   │
                         │                                       │
  ┌─────────────┐   ←→   │  exchange:orderbook:BTC-USD (TTL 10s)│
  │ WebSocket   │        │  exchange:ratelimit:tokens:{IP}       │
  │ Handler     │   ←→   │  exchange:ratelimit:ts:{IP}           │
  └─────────────┘   ←→   │  exchange:idempotency:{key} (TTL 24h)│
                         └──────────────────────────────────────┘
         ↑                             ↑                ↑
   讀快照(Cache Hit)            分散式限流           冪等性緩存
   撮合後寫入快照               Lua Script 原子操作    24小時 TTL
```

---

## 可靠性設計：Fail-Open vs Fail-Close

本系統 Redis 的所有使用，都選擇 **Fail-Open（開放失效）** 策略：

| 場景 | Redis 異常時的行為 | 理由 |
|------|-------------------|------|
| 訂單簿快取 | 降級到 In-Memory Engine | 資料更新、但不影響業務 |
| 限流器 | 放行所有請求 | 限流是安全網，不能成為可用性的瓶頸 |
| 冪等性 | 每次都執行業務邏輯 | 寧可少量重複，不可因此拒絕正常訂單 |

> 💡 **面試亮點**：在金融系統中，選擇 Fail-Open 還是 Fail-Close，取決於你對「可用性」與「一致性」的取捨（CAP 理論實踐）。限流和快取選 Fail-Open，涉及金錢的扣款則絕對要 Fail-Close（寧可報錯，不可少扣）。

---

👉 **下一篇**：[Kafka 事件驅動架構](07_kafka_event_driven.md) | **導覽**：[文件總覽 README](README.md)
