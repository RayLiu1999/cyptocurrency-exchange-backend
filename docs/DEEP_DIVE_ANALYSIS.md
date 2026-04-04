# 加密貨幣交易所後端 — 深入技術解析

> **文件定位**：本文件面向有意深入理解系統設計決策的工程師，內容涵蓋架構選型理由、核心演算法、分散式系統挑戰與解法。

---

## 目錄

1. [專案總覽](#1-專案總覽)
2. [架構演進：從單體到微服務](#2-架構演進從單體到微服務)
3. [Hexagonal Architecture（六角架構）](#3-hexagonal-architecture六角架構)
4. [領域模型設計](#4-領域模型設計)
5. [撮合引擎深度剖析](#5-撮合引擎深度剖析)
6. [原子結算事務（Atomic Settlement）](#6-原子結算事務atomic-settlement)
7. [Outbox Pattern — 消除雙寫風險](#7-outbox-pattern--消除雙寫風險)
8. [分散式 Leader Election — 防腦裂機制](#8-分散式-leader-election--防腦裂機制)
9. [Kafka 事件驅動架構](#9-kafka-事件驅動架構)
10. [WebSocket 即時推播設計](#10-websocket-即時推播設計)
11. [防死鎖策略](#11-防死鎖策略)
12. [冪等性設計（Idempotency）](#12-冪等性設計idempotency)
13. [資料庫設計決策](#13-資料庫設計決策)
14. [可觀測性（Observability）](#14-可觀測性observability)
15. [關鍵技術挑戰與解法彙整](#15-關鍵技術挑戰與解法彙整)

---

## 1. 專案總覽

| 項目 | 說明 |
|------|------|
| **語言** | Go 1.21+ |
| **Web 框架** | Gin |
| **資料庫** | PostgreSQL（pgx/v5 原生驅動） |
| **訊息佇列** | Apache Kafka（franz-go 客戶端） |
| **快取** | Redis |
| **即時通訊** | WebSocket（gorilla/websocket） |
| **精度計算** | shopspring/decimal（避免浮點誤差） |
| **日誌** | Uber Zap（結構化日誌） |
| **監控** | Prometheus + Grafana |

**核心能力**：
- 限價單 / 市價單撮合
- 帳戶資金的原子鎖定與結算
- 雙模式運行：**同步單體模式** ↔ **非同步微服務模式**（同一份 Core 程式碼）

---

## 2. 架構演進：從單體到微服務

專案採用**漸進式演進**策略，核心 Domain 程式碼只寫一次，透過依賴注入的方式切換運行模式。

```
┌─────────────────────────────────────────────────────────────────┐
│                     PHASE 1: 單體模式（Monolith）                │
│                                                                  │
│  HTTP Request → Gin Router → ExchangeServiceImpl                 │
│                               ↓                                  │
│                         記憶體撮合引擎 (同步)                     │
│                               ↓                                  │
│                         DB 事務結算 (COMMIT)                      │
│                               ↓                                  │
│                    WebSocket 推播（直接呼叫 OnTrade）             │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                 PHASE 2+: 微服務模式（Microservices）             │
│                                                                  │
│  order-service          matching-engine      settlement-service  │
│  ─────────────          ───────────────      ──────────────────  │
│  HTTP Request           Consumer:            Consumer:           │
│  → TX1：鎖金 + 建單      exchange.orders      exchange.settlements│
│  → Outbox 寫入 ──────→  → 記憶體撮合          → TX2：資金結算      │
│  → Outbox Worker        → Publish: settlements → Publish: updates │
│  → Kafka Produce         → Publish: orderbook  → WS 推播         │
│                                                                  │
│  共用：                                                          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │ internal/core/* (ExchangeServiceImpl, OrderService, ...) │    │
│  │ → eventBus != nil 時走非同步路徑                          │    │
│  │ → eventBus == nil 時走同步路徑                            │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

### 關鍵設計決策：雙模式相容

`ExchangeServiceImpl` 透過 **`eventBus EventPublisher`** 這個可選欄位，在啟動時決定運行模式：

```go
// exchange_service.go
type ExchangeServiceImpl struct {
    // ...
    eventBus  EventPublisher   // Kafka 事件發布 (可選，nil = 同步模式)
    outboxRepo *outbox.Repository // Outbox (可選，nil = 無可靠傳遞保護)
}
```

這使得同一份核心業務邏輯：
- **無需修改**即可在單元測試、整合測試、正式部署中使用
- **無需網路依賴**即可本地開發（不啟動 Kafka）
- 遵循 **Open/Closed Principle**：擴展開放（加 Kafka），修改封閉（Core 不變）

---

## 3. Hexagonal Architecture（六角架構）

專案嚴格遵循六角架構（又稱 Ports & Adapters），核心思想是 **業務邏輯與 I/O 完全解耦**。

```
┌─────────────────────────────────────────────────────┐
│                    外部配接器 (Adapters)               │
│  ┌──────────┐  ┌──────────┐  ┌─────────────────┐    │
│  │ Gin HTTP │  │ Kafka    │  │ PostgreSQL      │    │
│  │ Handlers │  │ Consumer │  │ Repository Impl │    │
│  └────┬─────┘  └────┬─────┘  └────────┬────────┘    │
│       │              │                  │             │
│  ┌────┴──────────────┴──────────────────┴──────────┐ │
│  │               Ports（介面）                      │ │
│  │  ExchangeService  OrderRepository  EventPublisher│ │
│  │  AccountRepository  CacheRepository  DBTransaction│ │
│  └────────────────────┬─────────────────────────────┘ │
│                       │                               │
│  ┌────────────────────┴─────────────────────────────┐ │
│  │          Domain Core（核心業務邏輯）               │ │
│  │  ExchangeServiceImpl / OrderService               │ │
│  │  MatchingEngine / SettlementLogic                 │ │
│  │  純 Go 結構體，無任何外部依賴                      │ │
│  └──────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

### Ports 定義（`internal/core/ports.go`）

| Port 介面 | 用途 | 實作位置 |
|-----------|------|----------|
| `OrderRepository` | 訂單 CRUD | `internal/repository/order_repository.go` |
| `AccountRepository` | 帳戶/餘額操作 | `internal/repository/account_repository.go` |
| `TradeRepository` | 成交記錄 + 冪等性檢查 | `internal/repository/trade_repository.go` |
| `EventPublisher` | 事件發布到 Kafka | `internal/infrastructure/kafka/` |
| `CacheRepository` | 訂單簿快照的 Redis 快取 | `internal/repository/cache_repository.go` |
| `DBTransaction` | 跨 Repo 事務封裝 | `internal/repository/postgres.go` |
| `MarketDataPublisher` | WebSocket 即時推播 | `internal/api/websocket_handler.go` |

> **好處**：測試時只需 Mock 介面，Core 邏輯可在無任何外部依賴下完整測試。

---

## 4. 領域模型設計

### 核心聚合體（`internal/core/domain.go`）

```
Account                    Order                       Trade
───────                    ─────                       ─────
id: UUID v7                id: UUID v7                 id: UUID v7
user_id: UUID              user_id: UUID               maker_order_id: UUID
currency: string           symbol: string              taker_order_id: UUID
balance: decimal           side: OrderSide (1/2)       symbol: string
locked: decimal            type: OrderType (1/2)       price: decimal
                           price: decimal              quantity: decimal
                           quantity: decimal           created_at: int64
                           filled_quantity: decimal
                           status: OrderStatus (1-5)
```

### 設計亮點

**1. 使用 UUID v7 代替 UUID v4**

```go
newID, err := uuid.NewV7()
```

UUID v7 包含毫秒級時間戳前綴，插入 PostgreSQL B-Tree 索引時是嚴格遞增的，**消除了隨機 UUID 導致的索引碎片化**（Index Fragmentation），大幅降低大量下單時的寫入放大問題。

**2. 使用 `shopspring/decimal` 代替 `float64`**

```go
Balance  decimal.Decimal // 精確計算，杜絕浮點誤差
Locked   decimal.Decimal // 0.1 + 0.2 = 0.3（不是 0.30000000000000004）
```

金融計算絕不能用浮點數。`decimal` 使用有理數運算，保證精度到小數點後 8 位。

**3. 訂單狀態機（State Machine）**

```
                ┌──────────────────────────────────┐
                │                                  ↓
NEW → PARTIALLY_FILLED ──→ FILLED              CANCELED
  └────────────────────────────────────────→  REJECTED
```

- `NEW`：訂單建立，資金已鎖定
- `PARTIALLY_FILLED`：部分成交，繼續掛在訂單簿等待
- `FILLED`：完全成交，結算完畢
- `CANCELED`：用戶撤單或市價單無流動性，資金已退還
- `REJECTED`：送單前驗證失敗（餘額不足等）

**4. 帳戶的雙欄位設計（Balance + Locked）**

```
balance: 可用餘額（用戶看到的）
locked:  鎖定餘額（下單後預扣，成交後轉出）
總資產 = balance + locked（在 DB CHECK 約束保護下永遠 ≥ 0）
```

這設計允許用戶「下掛單」時無需立刻扣款，僅鎖定等值資金，直到成交或撤單再做最終結算。

---

## 5. 撮合引擎深度剖析

撮合引擎（`internal/core/matching/`）是整個系統的核心計算單元。

### 資料結構

```
Engine
  └── OrderBook
        ├── bids []Order  // 買單：依價格降序排列（最高買價在前）
        └── asks []Order  // 賣單：依價格升序排列（最低賣價在前）
```

> **選型說明**：使用有序 Slice 而非 Priority Queue（heap）。在交易所場景下，最佳買/賣價通常在 Slice 頭部，時間複雜度 O(1)；插入為 O(n) 但 n（同一價位的掛單數）通常極小，實際效能優於 heap 的常數倍開銷。

### 撮合演算法（Price-Time Priority）

```go
// engine.go: Process() → matchBuyOrder()
func (e *Engine) matchBuyOrder(buyOrder *Order) []*Trade {
    for {
        bestAsk := e.orderBook.BestAsk()  // O(1)：取最低賣價
        if bestAsk == nil { break }        // 無賣單可匹配

        // === STP（Self-Trade Prevention） ===
        // 防止同一用戶的買賣單互相撮合（洗盤交易）
        if buyOrder.UserID == bestAsk.UserID {
            buyOrder.Quantity = decimal.Zero  // 放棄剩餘數量（Cancel Newest 策略）
            break
        }

        // 限價單：買價 >= 賣價才成交
        // 市價單：無條件成交（直接跳過價格檢查）
        if buyOrder.Type != TypeMarket && buyOrder.Price.LessThan(bestAsk.Price) {
            break
        }

        // 成交價格 = Maker 價格（交易所的價格優先原則）
        trade := &Trade{Price: bestAsk.Price, Quantity: min(buyOrder.Qty, askQty)}
    }
}
```

### 關鍵特性

| 特性 | 實作方式 |
|------|----------|
| **Price-Time Priority** | bids 降序、asks 升序存放，先進先出 |
| **STP（防洗盤）** | 偵測到 UserID 相同時，採 Cancel Newest 策略放棄新單剩餘量 |
| **市價單** | 不進入 OrderBook，吃完賣單後剩餘量直接丟棄 |
| **Goroutine 安全** | 整個 `Process()` 持有 `sync.Mutex`，串行執行 |
| **快照查詢** | `GetOrderBookSnapshot(depth)` 需持鎖，防止讀取到撮合中間狀態 |

### 冷啟動恢復（Engine Hydration）

```go
// exchange_service.go: RestoreEngineSnapshot()
func (s *ExchangeServiceImpl) RestoreEngineSnapshot(ctx context.Context) error {
    orders, _ := s.orderRepo.GetActiveOrders(ctx)  // 查詢所有 NEW/PARTIALLY_FILLED 的限價單
    for _, order := range orders {
        if order.Type != TypeLimit { continue }    // 市價單絕不恢復（這是防呆設計）
        engine := s.engineManager.GetEngine(order.Symbol)
        matchingOrder := &matching.Order{
            Quantity: order.Quantity.Sub(order.FilledQuantity), // 剩餘未成交量
        }
        engine.RestoreOrder(matchingOrder)  // 直接放入 OrderBook，不觸發撮合
    }
}
```

這解決了**服務崩潰後的記憶體狀態遺失**問題：重啟時從 PostgreSQL 完整重建訂單簿，確保記憶體引擎與 DB 狀態同步。

---

## 6. 原子結算事務（Atomic Settlement）

這是整個系統最複雜、最關鍵的邏輯，也是防止資金安全漏洞的核心防線。

### 問題背景

撮合引擎是記憶體操作（無鎖 Mutex），結算需要寫入 DB。若直接對每筆成交執行獨立事務，在高並發下會產生：
- **Lost Update**：兩個 Goroutine 同時讀到舊狀態後各自更新，最後一個寫入覆蓋前一個
- **Double Spend（雙重花費）**：同一筆資金被多次結算
- **Deadlock（死鎖）**：兩個事務以不同順序搶鎖

### 解法：Three-Phase Atomic Settlement

```
Phase 1: 資源排序 + 獲取排他鎖（SELECT ... FOR UPDATE）
─────────────────────────────────────────────────────────
allOrderIDs := [TakerID, Maker1ID, Maker2ID, ...]
// 關鍵：依 UUID 字串字典序排序
sort.Slice(allOrderIDs, func(i, j int) bool {
    return allOrderIDs[i].String() < allOrderIDs[j].String()
})
// 所有並發 Goroutine 看到的訂單鎖定順序永遠一致 → 死鎖不可能發生

for _, id := range allOrderIDs {
    lockedOrder, _ = orderRepo.GetOrderForUpdate(ctx, id) // SELECT FOR UPDATE
}

Phase 2: 純計算（無 DB 操作）
──────────────────────────────
// 計算每筆成交的資金變動 → 彙整成 []AccountUpdate
// 使用 AggregateAndSortAccountUpdates 去重 + 排序（防帳戶死鎖）

Phase 3: 批次寫入（順序一致）
──────────────────────────────
for _, id := range allOrderIDs { UpdateOrder() }  // 更新訂單狀態
for _, trade := range trades { CreateTrade() }     // 建立成交記錄
for _, update := range aggregated { UpdateBalance() } // 更新帳戶餘額
```

### 為什麼資金更新也需要排序？

```go
// exchange_service.go: AggregateAndSortAccountUpdates()
// 聚合：同一個 UserID + Currency 的多次變動合併成一次 DB UPDATE
// 排序：依 UserID + Currency 字典序，確保所有事務取帳戶鎖的順序一致
sort.Slice(result, func(i, j int) bool {
    if result[i].UserID.String() != result[j].UserID.String() {
        return result[i].UserID.String() < result[j].UserID.String()
    }
    return result[i].Currency < result[j].Currency
})
```

舉例：Taker 是 Alice，Maker 是 Bob
- 不排序：事務 A 先鎖 Alice-USD，再鎖 Bob-BTC
- 事務 B 同時先鎖 Bob-BTC，再鎖 Alice-USD → **死鎖**
- 排序後：所有事務永遠先鎖 Alice 再鎖 Bob → 死鎖**不可能**發生

### 市價單保證金退款機制

市價買單預扣 **105%** 作為緩衝（防止訂單簿快照瞬間過時）：

```
預扣 = estimateMarketBuyFunds(asks, quantity) × 1.05
實際花費 = Σ(成交價 × 成交量)
退款 = 預扣 - 實際花費
```

退款在 TX2 結算時原子退回 `balance`，確保多扣的資金不會遺失。

---

## 7. Outbox Pattern — 消除雙寫風險

### 問題：DB 與 Kafka 的一致性

```
❌ 危險的雙寫（可能丟失事件）：
TX1: { LockFunds + CreateOrder } → COMMIT
然後：eventBus.Publish(OrderPlacedEvent)  ← 若此時 Kafka 超時，事件永久丟失！
```

若 TX1 成功但 Kafka Publish 失敗，撮合引擎永遠不會收到這筆訂單，資金被鎖定但永遠無法結算。

### 解法：Transactional Outbox

```
✅ Outbox Pattern（事件不會遺失）：
TX1: {
    LockFunds()
    CreateOrder()
    outboxRepo.Insert(OrderPlacedEvent)  ← 與業務操作同一個 DB Transaction!
} → COMMIT（三者原子成功）

異步：OutboxWorker（每 3 秒掃描）
    FetchPending(batchSize=100)
    for each msg:
        publisher.PublishRaw(topic, partitionKey, payload) → Kafka
        repo.MarkPublished(msg.ID)
```

### 資料庫設計

```sql
-- 001_create_outbox_messages.sql
CREATE TABLE outbox_messages (
    id              UUID     -- UUID v7，B-Tree 友好
    aggregate_id    VARCHAR  -- 業務 ID（OrderID）
    topic           VARCHAR  -- 目標 Kafka Topic
    partition_key   VARCHAR  -- Kafka Partition Key（Symbol）
    payload         BYTEA    -- JSON 序列化的事件
    status          SMALLINT -- 0=Pending, 1=Published
    retry_count     INT      -- 重試計數（可接 DLQ）
    created_at      BIGINT   -- 建立時間
    published_at    BIGINT   -- 成功發送時間
);

-- 效能關鍵：Partial Index 只掃描 Pending 訊息
CREATE INDEX idx_outbox_messages_status_created_at
    ON outbox_messages (status, created_at)
    WHERE status = 0;  -- Partial Index：只索引 Pending 的資料
```

> **Partial Index** 是一個關鍵優化：歷史已發送的訊息（status=1）不進入索引，Worker 的 `SELECT WHERE status=0` 查詢效率不隨資料量增長而退化。

### 保證語意：At-Least-Once Delivery

Outbox 保證「至少一次」送達，**不保證「恰好一次」**。消費端（Settlement Consumer）必須實作冪等性保護（見第 12 節）。

---

## 8. 分散式 Leader Election — 防腦裂機制

### 問題：Kafka Partition 的排他消費

微服務水平擴展時，多個 matching-engine 實例同時消費 `exchange.orders` 的同一個 Partition，會導致同一筆訂單被多次撮合。

### 解法：PostgreSQL-based Leader Election + Fencing Token

```
                 ┌───────────────────────────┐
                 │  partition_leader_locks    │
                 │  partition: "orders:BTC"  │
                 │  leader_id: "pod-abc"      │
                 │  fencing_token: 5          │  ← 單調遞增
                 │  expires_at: 1711600000000 │  ← TTL 15 秒
                 └───────────────────────────┘
                           ↑
              ┌────────────┼────────────┐
              │            │            │
           Pod A        Pod B        Pod C
          (Leader)     (Standby)    (Standby)
          Token=5      嘗試競選      嘗試競選
          每5秒延租     (expires>now失敗)
```

### 選主邏輯（Upsert + WHERE）

```sql
-- 核心 SQL（_repository.go: AcquireLock）
INSERT INTO partition_leader_locks (partition, leader_id, fencing_token, expires_at)
VALUES ($1, $2, 1, $3)
ON CONFLICT (partition) DO UPDATE
  SET leader_id = EXCLUDED.leader_id,
      fencing_token = partition_leader_locks.fencing_token + 1,
      expires_at = EXCLUDED.expires_at
  WHERE partition_leader_locks.expires_at < NOW()  -- 只有租約過期才允許覆蓋
RETURNING fencing_token
```

### Fencing Token 防腦裂

```
場景：網路分區後舊 Leader 復活
─────────────────────────────
1. Leader A (token=5) 網路斷線，租約過期
2. Leader B 競選成功，token 遞增為 6
3. A 網路恢復，嘗試 ExtendLease(token=5)
4. SQL: UPDATE ... WHERE fencing_token = 5
   → DB 現在是 6，WHERE 不符，0 rows affected
   → A 偵測到延租失敗，主動退回 Standby
5. 腦裂（兩個 Leader 同時消費）不可能發生
```

### Elector 生命週期

```go
// elector.go: Run()
elector.Run(ctx,
    onBecomeLeader: func() {
        kafkaConsumer.StartConsuming()  // 開始消費 Kafka Partition
    },
    onLoseLeadership: func() {
        kafkaConsumer.StopConsuming()   // 立即停止消費
    },
)
```

優雅關機時主動呼叫 `ReleaseLock()`，加速備機接管，減少服務中斷時間。

---

## 9. Kafka 事件驅動架構

### Topic 設計

| Topic | 用途 | Partition Key | 生產者 | 消費者 |
|-------|------|--------------|--------|--------|
| `exchange.orders` | 下單/撤單命令 | `symbol` | order-service | matching-engine |
| `exchange.settlements` | 結算請求 | `symbol` | matching-engine | settlement-service |
| `exchange.trades` | 個別成交事件 | `symbol` | matching-engine | market-data-service |
| `exchange.orderbook` | 掛單簿快照 | `symbol` | matching-engine | order-service (WS) |
| `exchange.order_updates` | 訂單狀態更新 | `symbol` | settlement-service | order-service (WS) |

### 為什麼 `exchange.orders` 同一 Topic 同時處理下單和撤單？

```go
// events.go 中的關鍵設計原則：

// ⚠️ 重要：OrderCancelRequestedEvent 必須與 OrderPlacedEvent 走同一 Topic + Partition
// 原因：Kafka 在同一 Partition 內保證嚴格有序
// 這樣 matching_consumer 才能保證：
//   「先處理下單（把訂單放入 OrderBook）」
//   「再處理撤單（從 OrderBook 移除）」
//
// 若走不同 Topic：可能出現「撤一個不存在的訂單」的競態條件
```

### 消費者設計：先路由，再解碼

```go
// matching_consumer.go: HandleMatchingEvent()
// 第一步：只解碼 EventType 決定路由（避免重複完整解析）
var envelope struct { EventType EventType `json:"event_type"` }
json.Unmarshal(value, &envelope)

switch envelope.EventType {
case EventOrderPlaced:
    // 第二步：才做完整解碼（避免無謂的 CPU 消耗）
    var event OrderPlacedEvent
    json.Unmarshal(value, &event)
    return s.handleOrderPlaced(ctx, &event)
}
```

### 撮合引擎的無限重試設計

```go
// matching_consumer.go: handleOrderPlaced()
// 撮合完成後，必須發布 SettlementRequestedEvent
// 若 Kafka 短暫不可用，絕不 return error（那會導致 Consumer 重新消費同一條消息，重複撮合！）
for {
    err := s.eventBus.Publish(ctx, TopicSettlements, symbol, settlementEvent)
    if err == nil { break }
    if ctx.Err() != nil { return ctx.Err() } // 優雅關機例外
    time.Sleep(1 * time.Second)               // 等待後重試
}
```

> **設計哲學**：撮合是純記憶體操作，一旦完成就不可重做。若因 Kafka 失敗而讓 Consumer Rollback Offset，重新消費會觸發**二次撮合**，造成結算金額翻倍。因此寧可無限重試，也不能 return error。

---

## 10. WebSocket 即時推播設計

### Hub-and-Spoke + CSP 模型

```
  成交事件 (OnTrade)
  訂單更新 (OnOrderUpdate)     → [broadcast channel, cap=256]
  訂單簿更新 (OnOrderBookUpdate)           ↓
                                    Hub.Run() goroutine
                                    （唯一讀寫 clients map）
                                           ↓
                              for each client:
                              client.send <- payload  (non-blocking)
                                    ↓                       ↓
                              writePump goroutine      channel full → 踢除該連線
                              (每個 Client 獨立)        (避免慢速客戶端拖垮廣播)
```

### 關鍵設計：分離廣播與網路 I/O

```go
// websocket_handler.go

// 廣播層：Non-blocking，不阻塞業務邏輯（尤其是 DB Transaction 中呼叫的 OnTrade）
func (h *WebSocketHandler) Broadcast(message []byte, messageType string) {
    select {
    case h.broadcast <- outboundMessage{...}:  // 放入 channel
    default:                                    // channel 滿了 → 丟棄（可接受的 loss）
    }
}

// 寫入層：Client 的 writePump goroutine 負責真正的網路 I/O（可能阻塞）
func (c *Client) writePump() {
    for {
        message := <-c.send          // 從專屬 channel 取出
        c.conn.WriteMessage(message) // 真正寫入網路（可能阻塞，但只影響這個 Client）
    }
}
```

這確保了：
- **廣播** 永遠是 O(1) 非阻塞操作，不影響撮合路徑
- **慢速客戶端**（WriteTimeout 或網路差）只剔除自己，不影響其他連線
- Hub goroutine **無需 Mutex**，`clients map` 只在 Hub 內讀寫（CSP 原則）

---

## 11. 防死鎖策略

專案在三個層面實作了防死鎖設計：

### 層面一：訂單鎖定順序（Order Lock Ordering）

```go
// order_service.go / settlement_consumer.go
// 無論 Taker/Maker 是誰，所有並發事務按相同的 UUID 字典序加鎖
sort.Slice(allOrderIDs, func(i, j int) bool {
    return allOrderIDs[i].String() < allOrderIDs[j].String()
})
// 結果：死鎖的必要條件「循環等待」被消除
```

### 層面二：帳戶更新排序（Account Update Ordering）

```go
// exchange_service.go: AggregateAndSortAccountUpdates()
// 同理，帳戶更新按 UserID + Currency 字典序排序
// 任何交易的買方/賣方，其帳戶更新順序全域一致
```

### 層面三：保守的資源獲取（Conservative Locking）

撤單操作採用**先快速檢查、再事務鎖定**的兩段式設計：

```go
// order_service.go: CancelOrder()
// 第一次（無鎖）：快速校驗權限與初始狀態
orderPreCheck, _ := s.orderRepo.GetOrder(ctx, orderID)
if orderPreCheck.Status == StatusFilled { return error } // Early return

// 事務內（FOR UPDATE）：重新驗證（可能在等鎖期間已被撮合）
err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
    order, _ := s.orderRepo.GetOrderForUpdate(ctx, orderID)
    if order.Status == StatusFilled { return error } // 防止競態
    ...
})
```

---

## 12. 冪等性設計（Idempotency）

Kafka 的 At-Least-Once 語意意味著同一條消息可能被重複消費（Consumer Rebalance、網路重試等）。系統在多個層面保護冪等性。

### 層面一：Trade 存在性檢查

```go
// settlement_consumer.go: HandleSettlementEvent()
// 以第一筆 TradeID 作為冪等鍵（UUID v7，由撮合引擎生成）
if len(event.Trades) > 0 {
    exists, _ := s.tradeRepo.TradeExistsByID(ctx, event.Trades[0].ID)
    if exists { return nil }  // 已結算，直接跳過
}
```

### 層面二：TX 內二次確認（TOCTOU 防護）

```go
// settlement_consumer.go: executeSettlementTx()
// 取得排他鎖後，再次確認訂單狀態（防止 Check-Then-Act 競態）
takerOrder := lockedOrders[event.TakerOrderID]
if takerOrder.Status != StatusNew {
    return ErrIdempotencySkip  // 在事務內偵測到已處理
}
```

### 層面三：無成交訂單的冪等保護

```go
// 沒有成交記錄時，無法用 TradeID 判斷，改用訂單狀態
takerOrder, _ := s.orderRepo.GetOrder(ctx, event.TakerOrderID)
if takerOrder.Status != StatusNew { return nil }  // 已退款，跳過
```

### ErrIdempotencySkip 的設計

```go
var ErrIdempotencySkip = fmt.Errorf("idempotency skip: event already processed")

// 呼叫端：
if errors.Is(err, ErrIdempotencySkip) {
    return nil  // 不視為錯誤，Kafka Commit 正常 Offset
}
```

`ErrIdempotencySkip` 是 **業務正常行為**，不是錯誤。讓 Consumer 正常 Commit offset，不觸發重試計數器。

---

## 13. 資料庫設計決策

### 時間戳：Unix 毫秒（int64）vs TIMESTAMPTZ

```sql
created_at BIGINT NOT NULL  -- Unix 毫秒
```

選擇 `BIGINT` 而非 `TIMESTAMPTZ` 的理由：
1. Go 的 `time.Now().UnixMilli()` 直接產生，無需 DB 時區轉換
2. 索引效能：整數比較快於 timestamp 比較
3. JSON 序列化：直接輸出數字，前端自行轉換

### CHECK 約束（業務規則的最後防線）

```sql
-- schema.sql
CHECK (balance >= 0)  -- 餘額不能為負數
CHECK (locked >= 0)   -- 鎖定金額不能為負數
```

即使應用層出現 Bug，DB 約束會拒絕不合理的資料寫入，提供最後一道安全防線。

### 複合索引（Composite Index）

```sql
-- 供撮合引擎冷啟動時讀取活動訂單使用
CREATE INDEX idx_orders_symbol_side_price ON orders(symbol, side, price);
```

撮合引擎恢復時的查詢：`WHERE symbol='BTC-USD' AND status IN (1,2) ORDER BY side, price`，這個複合索引可以覆蓋整個查詢（Index Only Scan）。

### Decimal 精度

```sql
price    DECIMAL(20, 8)  -- 最多 20 位有效數字，小數後 8 位
quantity DECIMAL(20, 8)
balance  DECIMAL(20, 8)
```

`DECIMAL(20, 8)` 支援最大 999,999,999,999.00000000 的金額，足以應對任何加密貨幣的市值範圍。

---

## 14. 可觀測性（Observability）

### Prometheus Metrics 分類

```go
// metrics.go 定義的關鍵指標：

// 1. 業務指標
ObserveOrder(mode, side, orderType, err, duration)  // 下單延遲分佈
AddTradesExecuted(mode, count)                       // 成交筆數計數器

// 2. Kafka Consumer 指標
ObserveKafkaEvent(handler, topic, err, duration)     // 各類事件處理延遲

// 3. Outbox Worker 指標
SetOutboxPendingCount(count)                          // 積壓量（用於 Grafana Alert）
ObserveOutboxPublish(status, latency)                 // 發送成功/失敗比率

// 4. Leader Election 指標
SetPartitionLeader(partition, isLeader bool)          // 哪個實例是 Leader
ObserveLeaderRenewal(status)                          // 租約延長成功/失敗率

// 5. WebSocket 指標
WebSocketConnected(serviceName)                       // 連線數
RecordWebSocketBroadcast(service, msgType, result)   // 廣播成功/丟棄率
```

### 關鍵監控點

| 指標 | 告警條件 | 意義 |
|------|----------|------|
| `outbox_pending_count` | > 100 持續 5 分鐘 | Kafka 可能不可用或 Worker 崩潰 |
| `leader_renewal_errors` | > 3 次/分鐘 | DB 連線問題或網路分區 |
| `order_latency_p99` | > 500ms | 系統過載或 DB 慢查詢 |
| `ws_broadcast_dropped` | > 10% | WebSocket 廣播 Channel 滿，需擴容 |

---

## 15. 關鍵技術挑戰與解法彙整

| 挑戰 | 風險 | 解法 | 所在檔案 |
|------|------|------|----------|
| 記憶體引擎與 DB 的一致性（Commit Timing Anomaly） | 訂單被撮合前 DB 未 COMMIT | TX1 成功後才送入引擎 | `order_service.go:135` |
| 多 Maker 並發結算的死鎖 | 事務 A、B 互相等待行鎖 | UUID 字典序排序後統一加鎖 | `order_service.go:167` |
| 帳戶更新死鎖 | 買賣雙方帳戶以不同順序鎖定 | AggregateAndSortAccountUpdates | `exchange_service.go:43` |
| DB 與 Kafka 雙寫一致性 | TX1 成功但 Kafka 失敗，訂單消失 | Transactional Outbox Pattern | `outbox/worker.go` |
| 微服務下 OrderBook 的多副本問題 | 多個引擎實例分別處理同一 Partition | Leader Election + Fencing Token | `election/elector.go` |
| Kafka At-Least-Once 重複消費 | 同一筆成交被結算兩次 | 三層冪等性保護 | `settlement_consumer.go` |
| 撮合引擎崩潰後狀態遺失 | 記憶體訂單簿無法恢復 | 冷啟動从 DB Hydration | `exchange_service.go:162` |
| 市價單資金預扣不準確 | 訂單簿瞬間變化導致資金不足 | 預扣 105% + TX2 退款 | `order_service.go:457` |
| WebSocket 慢速客戶端拖垮廣播 | 一個慢連線阻塞所有推播 | Per-client channel + 滿了踢除 | `websocket_handler.go:151` |
| STP 洗盤交易防護 | 用戶自買自賣操縱行情 | Cancel Newest 策略 | `engine.go:73` |

---

*本文件最後更新：2026-03-28*
*對應程式碼版本：`internal/core` 包含 settlement_consumer.go、order_service.go、exchange_service.go*
