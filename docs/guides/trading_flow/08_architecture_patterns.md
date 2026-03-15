# 架構設計模式 (8)：貫穿全系統的設計哲學

> **本文摘要**：本文記錄了在整個系統中反覆出現的跨切面設計模式——這些不屬於任何單一功能，卻決定了程式碼的整體質量。這些模式是面試中最能展現「系統思維」的部分。

---

## 一、六角架構（Ports & Adapters）

本系統的核心設計原則：**Core 層完全不知道「底層用了什麼」**。

### 具體表現

```
internal/
├── core/
│   ├── ports.go          ← 只有介面定義（Ports）
│   ├── exchange_service.go
│   ├── order_service.go
│   └── matching/         ← 純業務邏輯，0 個外部 import
│
├── repository/           ← Adapters（PostgreSQL 實作 ports.go 裡的介面）
├── infrastructure/
│   ├── kafka/            ← Adapters（Kafka 實作 EventPublisher 介面）
│   └── redis/            ← Adapters（Redis 實作 CacheRepository 介面）
└── api/                  ← Adapters（HTTP 呼叫 Core Service）
```

### `ports.go`：Core 層的合約邊界

```go
// internal/core/ports.go
// 所有介面都在這裡定義，Core 服務只依賴這些介面

type OrderRepository interface {
    CreateOrder(ctx context.Context, order *Order) error
    GetOrder(ctx context.Context, id uuid.UUID) (*Order, error)
    GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*Order, error)  // FOR UPDATE
    UpdateOrder(ctx context.Context, order *Order) error
    // ...
}

type EventPublisher interface {
    Publish(ctx context.Context, topic, key string, payload interface{}) error
    Close()
}

// ⭐ CacheRepository 讓 Redis 變成「可插拔」的組件
type CacheRepository interface {
    GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error)
    SetOrderBook(ctx context.Context, symbol string, snapshot *matching.OrderBookSnapshot) error
}
```

### 為什麼這樣設計？

```go
// ExchangeServiceImpl 的欄位：全部都是介面，不是具體型別
type ExchangeServiceImpl struct {
    orderRepo   OrderRepository   // 不是 *pgxpool.Pool
    accountRepo AccountRepository // 不是 *redis.Client
    cacheRepo   CacheRepository   // 不是 *RedisCacheRepository
    eventBus    EventPublisher    // 不是 *kafka.Producer
}
```

**好處：**
1. **測試**：Mock 介面替換，不需要真實 DB/Redis/Kafka 就能單元測試 Core 層
2. **可替換**：想換 Redis → Memcached？只需實作 `CacheRepository`，Core 一行不改
3. **漸進降級**：`eventBus == nil` 時自動降級到同步模式（見 07 Kafka 文件 §八）

---

## 二、HTTP 冪等性 Middleware

### 什麼問題？

前端在網路不穩時，可能對同一個「下單請求」重試多次。如果每次都成功建立訂單，使用者就多買了幾倍。

### 解法：Idempotency-Key Header

```
前端發出請求時，帶上一個客戶端自己產生的 UUID：
POST /api/orders
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
```

### Middleware 的工作原理

```go
// internal/middleware/idempotency.go

func IdempotencyMiddleware(store IdempotencyStore, ttl time.Duration) gin.HandlerFunc {
    return func(c *gin.Context) {
        key := c.GetHeader("Idempotency-Key")
        if key == "" {
            c.Next()  // 沒帶 Key，視為不需要冪等保護，放行
            return
        }

        // 快取命中：直接回傳之前的結果，Handler 完全不執行
        if entry := store.Get(key); entry != nil {
            c.Data(entry.StatusCode, "application/json", entry.Body)
            c.Abort()
            return
        }

        // 快取未命中：用 bodyLogWriter「偷聽」Handler 的回應
        blw := &bodyLogWriter{ResponseWriter: c.Writer}
        c.Writer = blw
        c.Next()

        // Handler 完成後，只快取成功的回應（4xx/5xx 不應該被重用）
        if blw.statusCode < 400 && len(blw.body) > 0 {
            store.Set(key, blw.statusCode, blw.body, ttl)
        }
    }
}
```

### bodyLogWriter：攔截 Response 的技巧

```go
// 包裝 gin.ResponseWriter，在「轉發給 Client 的同時」複製一份給快取
type bodyLogWriter struct {
    gin.ResponseWriter
    statusCode int
    body       []byte
}

func (w *bodyLogWriter) Write(b []byte) (int, error) {
    if w.statusCode == 0 {
        w.statusCode = http.StatusOK
    }
    w.body = append(w.body, b...)         // 複製一份
    return w.ResponseWriter.Write(b)       // 同時傳給 Client
}
```

> **面試亮點**：這裡有個常見 Bug：只攔截但忘記把 `b` append 到 `w.body`，導致快取存的永遠是空 body。本系統的 `Write` 方法同時做了兩件事（傳給 Client + 存快取），確保資料一致。

### 兩種 Store：記憶體 vs Redis

```go
// IdempotencyStore 介面（又是一個 Port！）
type IdempotencyStore interface {
    Get(key string) *idempotencyEntry
    Set(key string, statusCode int, body []byte, ttl time.Duration)
}

// 單機使用：Memory Store（內建 cleanup goroutine 防洩漏）
func NewMemoryIdempotencyStore() IdempotencyStore {
    s := &memoryIdempotencyStore{store: make(map[string]*idempotencyEntry)}
    go s.cleanupLoop()  // 每 10 分鐘清理過期條目
    return s
}

// 分散式使用：Redis Store（多台 Server 共享同一個冪等性狀態）
// → 見 internal/middleware/redis_idempotency_store.go
// key 格式：idempotency:{Idempotency-Key} (TTL = 24 小時)
```

---

## 三、UUID v7：時間可排序的主鍵

### 為什麼不用自增 ID（AUTO_INCREMENT）？

| | 自增 INT | UUID v4 | **UUID v7** |
|--|---------|---------|------------|
| 全域唯一 | ❌（分散式衝突） | ✅ | ✅ |
| 含時間資訊 | ❌ | ❌ | ✅ |
| 索引友好（有序） | ✅ | ❌（隨機插入） | ✅（時間遞增） |
| 可以估算建立時間 | ❌ | ❌ | ✅ |

```go
// 本系統使用 github.com/google/uuid
orderID, err := uuid.NewV7()  // 格式：timestamp + random bits

// UUID v7 的前段是毫秒時間戳，所以「字典序排列 = 時間排列」
// 這讓我們可以用 UUID 的字串排序作為 Two-Phase Locking 的穩定排序鍵
sort.Slice(allOrderIDs, func(i, j int) bool {
    return allOrderIDs[i].String() < allOrderIDs[j].String()
})
```

> **延伸意義**：在 TX2 結算時，我們的排序鍵 = UUID 字典序，而 UUID v7 的字典序 ≈ 建立時間序。這讓鎖定順序同時也代表「訂單建立的先後」，在概念上更合理。

---

## 四、Decimal 精度：金融計算不能用浮點數

```go
// ❌ 永遠不要這樣
price := 0.1 + 0.2  // = 0.30000000000000004 (浮點誤差)

// ✅ 本系統使用 shopspring/decimal
import "github.com/shopspring/decimal"
price := decimal.NewFromFloat(0.1).Add(decimal.NewFromFloat(0.2))
// = "0.3" (精確)
```

### 資料庫 Schema 中的精度定義

```sql
-- DECIMAL(20, 8) 代表：最多 20 位數字，小數點後固定 8 位
balance  DECIMAL(20, 8) NOT NULL DEFAULT 0
-- 最大值：999,999,999,999.99999999
-- 最小精度：0.00000001 (1 聰 / 1 satoshi)
```

### 統一時間格式：Unix 毫秒（int64）

```sql
-- ❌ 不使用 TIMESTAMP WITH TIME ZONE（時區問題複雜）
-- ✅ 全部使用 int64 毫秒
created_at BIGINT NOT NULL  -- Go 端：time.Now().UnixMilli()
updated_at BIGINT NOT NULL
```

**好處：**
- 跨語言一致（Go、JavaScript 都能直接使用數字，無需解析時區）
- 排序和比較只需整數比較，不涉及時區轉換
- JSON 序列化更小（數字比字串佔用空間少）

---

## 五、Two-Phase Locking（兩階段鎖定）防死鎖

這個模式在 03、05、07 文件中都有觸及，這裡整合說明。

### 核心函式：聚合、過濾、排序

```go
// internal/core/exchange_service.go
func AggregateAndSortAccountUpdates(updates []AccountUpdate) []AccountUpdate {

    // 階段 1：聚合（同一 UserID + Currency 合併）
    aggMap := make(map[string]*AccountUpdate)
    for _, up := range updates {
        key := up.UserID.String() + "_" + up.Currency
        if existing, ok := aggMap[key]; ok {
            existing.Amount = existing.Amount.Add(up.Amount)
            existing.Unlock = existing.Unlock.Add(up.Unlock)
        } else {
            copyUp := up
            aggMap[key] = &copyUp
        }
    }

    // 階段 2：過濾（Amount 和 Unlock 都是 0 的記錄不需要寫 DB）
    var result []AccountUpdate
    for _, ptr := range aggMap {
        if !ptr.Amount.IsZero() || !ptr.Unlock.IsZero() {
            result = append(result, *ptr)
        }
    }

    // 階段 3：排序（全域統一鎖定順序，根絕死鎖）
    sort.Slice(result, func(i, j int) bool {
        if result[i].UserID.String() != result[j].UserID.String() {
            return result[i].UserID.String() < result[j].UserID.String()
        }
        return result[i].Currency < result[j].Currency
    })

    return result
}
```

### 三個階段的設計理由

```
聚合（Aggregate）
└→ 原因：一個 Taker 可能同時吃掉 Maker1 的 BTC 和 Maker2 的 BTC
         若不聚合就分兩次更新 Balance，效率差且 race condition 風險更高

過濾（Filter）
└→ 原因：某些情況下一筆成交的淨 Balance 變動為 0（例如自己掛、自己撤）
         寫 DB 会浪費 IO

排序（Sort by UUID）
└→ 原因：死鎖的充要條件是「循環等待」
         全域統一鎖順序 → 不存在循環 → 不可能死鎖
         （Two-Phase Locking 的核心定理）
```

---

## 六、STP（Self-Trade Prevention）：避免左手換右手

```go
// internal/core/matching/engine.go
func (e *Engine) matchBuyOrder(buyOrder *Order) []*Trade {
    for {
        bestAsk := e.orderBook.BestAsk()
        if bestAsk == nil { break }

        // ⭐ STP 檢查：同一個使用者不能跟自己成交
        if buyOrder.UserID == bestAsk.UserID {
            // 採用 Cancel Newest 策略：讓新訂單剩餘量歸零，退出撮合迴圈
            // （不移除 Maker 訂單，只放棄 Taker 剩餘量）
            buyOrder.Quantity = decimal.Zero
            break
        }
        // ...
    }
}
```

**為什麼要防 STP？**
- **市場操縱防護**：惡意使用者不能用「左手賣給右手」來製造虛假成交量
- **Crossed Book 防護**：若 buyPrice >= askPrice 但雙方相同，成交本身無意義
- **Cancel Newest 策略**：放棄新進入的訂單（Taker），保留掛單簿中已存在的訂單（Maker）

---

## 七、面試重點整理

| 主題 | 面試問法 | 本系統的答案 |
|------|---------|------------|
| **架構選擇** | 為什麼用六角架構而不是直接 DI？ | Core 對 Kafka/Redis 毫無感知，可獨立測試，也可降級 |
| **冪等性** | 網路重試怎麼防止雙重下單？ | Idempotency-Key Header + Redis 24h 快取 |
| **主鍵選擇** | 為什麼用 UUID 不用自增 ID？ | UUID v7 時間有序 + 分散式無衝突 |
| **金融精度** | 為什麼不用 float？ | 浮點誤差不可接受；Decimal(20,8) + shopspring/decimal |
| **STP** | 用戶能否跟自己成交？ | Cancel Newest 策略，STP 在撮合引擎層阻擋 |
| **死鎖消除** | 怎麼設計才能從根本消除死鎖？ | 統一排序鍵（UUID 字典序）= 統一鎖順序 = 無循環等待 |

---

👉 **回到開始**：[HTTP 請求與訂單建立](01_order_creation.md) | **導覽**：[文件總覽 README](README.md)
