# 基礎設施解析 (7)：Kafka 事件驅動架構

> **本文摘要**：引入 Kafka (Redpanda) 後，「下單」這個動作從單一同步函式，演變成一個橫跨三個微服務的非同步事件流。本文深入解析每一個設計決策背後的理由，以及「可靠性」是如何被一層層構建起來的。

---

## 一、為什麼需要 Kafka？同步模式的瓶頸

在第一版（無 Kafka）的同步架構中，`PlaceOrder` 是一個「大巨人函式」：

```
HTTP 請求 → TX1 (鎖倉+建單) → 撮合引擎 → TX2 (結算) → HTTP 回應
                 ↑____________________________________↑
                         全程阻塞 HTTP Handler
```

**痛點：**
- HTTP Handler 要等待整個撮合+結算完成才能回應（延遲高）
- 撮合引擎（記憶體）與結算（DB）緊耦合，無法獨立擴展
- 任何一個環節失敗，使用者就等到 timeout

**引入 Kafka 後：**
```
HTTP 請求 → TX1 (鎖倉+建單) → 發布事件 → HTTP 202 Accepted (快速回應)
                                    ↓
                           Kafka Topic: exchange.orders
                                    ↓
                          matching-engine Consumer
                          (非同步撮合)
                                    ↓
                           Kafka Topic: exchange.settlements
                                    ↓
                          settlement-engine Consumer
                          (非同步結算)
                                    ↓
                          WebSocket 推播結果給前端
```

> **核心原則**：HTTP Handler 只負責「接收請求 + 鎖定資金」這兩件責任最重的事，然後把控制權交出去。使用者得到 `202 Accepted`，代表「我已收到你的請求，正在處理中」。

---

## 二、事件定義：Core 層的合約 (`core/events.go`)

事件定義放在 **Core 層**，這是六角架構的關鍵設計：Core 不依賴 Kafka，Kafka 只是事件的「搬運工」。

```go
// internal/core/events.go

// --- Topic 常數（屬於業務語言，放在 Core）---
const (
    TopicOrders      = "exchange.orders"      // 下單 & 撤單命令
    TopicSettlements = "exchange.settlements" // 撮合完成後的結算請求
    TopicTrades      = "exchange.trades"      // 個別成交事件（供外部訂閱）
    TopicOrderBook   = "exchange.orderbook"   // 掛單簿快照更新
)

// OrderPlacedEvent - TX1 完成後發布
// 攜帶 AmountLocked，讓後續結算知道「預扣了多少錢」（市價單退款計算用）
type OrderPlacedEvent struct {
    EventType      EventType       `json:"event_type"`
    Symbol         string          `json:"symbol"`       // Partition Key
    OrderID        uuid.UUID       `json:"order_id"`
    AmountLocked   decimal.Decimal `json:"amount_locked"`   // TX1 鎖定金額
    LockedCurrency string          `json:"locked_currency"` // 鎖定幣種
    // ... 其他欄位
}

// SettlementRequestedEvent - 撮合完成後發布
// 攜帶完整的 Trade 列表，讓 settlement consumer 一次性完成 TX2
type SettlementRequestedEvent struct {
    EventType      EventType         `json:"event_type"`
    Symbol         string            `json:"symbol"`
    TakerOrderID   uuid.UUID         `json:"taker_order_id"`
    AmountLocked   decimal.Decimal   `json:"amount_locked"` // 從 TX1 傳遞而來
    RemainingQty   decimal.Decimal   `json:"remaining_qty"` // 撮合後剩餘，STP 偵測用
    Trades         []*matching.Trade `json:"trades"`        // 所有成交記錄
}
```

### EventPublisher 介面（依賴反轉）

```go
// internal/core/ports.go
// Core 層只看到這個介面，完全不知道底層是 Kafka 還是其他 MQ
type EventPublisher interface {
    Publish(ctx context.Context, topic, key string, payload interface{}) error
    Close()
}
```

---

## 三、Producer 設計：Publish Timeout 防止 HTTP 雪崩

```go
// internal/infrastructure/kafka/producer.go

func (p *Producer) Publish(ctx context.Context, topic, key string, payload interface{}) error {
    // 加入 PublishTimeout 子 Context，防止 Kafka 不可用時讓 HTTP Handler 永遠等待
    // 預設 2 秒：超過則放棄本次發布，而非永遠阻塞
    pubCtx, cancel := context.WithTimeout(ctx, p.publishTimeout)
    defer cancel()

    data, err := json.Marshal(payload)
    if err != nil { ... }

    record := &kgo.Record{
        Topic: topic,
        Key:   []byte(key), // Partition Key = Symbol，保證同一交易對有序
        Value: data,
    }

    // ProduceSync 等待 Broker ACK，確認訊息確實寫入
    if err := p.client.ProduceSync(pubCtx, record).FirstErr(); err != nil {
        logger.Error("發布 Kafka 事件失敗", ...)
        return fmt.Errorf("發布事件失敗: %w", err)
    }
    return nil
}
```

**Partition Key = Symbol 的設計理由：**
```
Partition 0: BTC-USD 的所有事件 (嚴格有序)
Partition 1: ETH-USD 的所有事件 (嚴格有序)
Partition 2: SOL-USD 的所有事件 (嚴格有序)

→ 同一交易對的訂單永遠按發出順序處理
→ 不同交易對可以並行處理（不同 Partition 可以分配給不同 Consumer）
→ 保證「先下單 → 再撤單」的語意不會被打亂
```

---

## 四、Consumer 設計：可靠性的三個護城河

### 護城河 1：關閉 Auto-Commit，手動 Commit

```go
// internal/infrastructure/kafka/consumer.go
func NewConsumer(cfg Config, groupID string, topics []string) (*Consumer, error) {
    client, err := kgo.NewClient(
        kgo.SeedBrokers(cfg.Brokers...),
        kgo.ConsumerGroup(groupID),
        kgo.ConsumeTopics(topics...),
        kgo.DisableAutoCommit(),  // ← 關鍵！
        // 從最早的 Offset 開始（重啟後不遺漏任何訊息）
        kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
    )
    // ...
}
```

**為什麼關閉 Auto-Commit？**
```
❌ Auto-Commit 的危險：
1. 讀到訊息 (Offset: 100)
2. Auto-Commit: 告訴 Broker「我已處理到 100」
3. Handler 執行 DB 結算 → 資料庫崩潰！
4. 重啟後，從 Offset 101 繼續，❌ 100 那筆訊息永遠丟失

✅ Manual Commit：
1. 讀到訊息 (Offset: 100)
2. Handler 執行 DB 結算 → 成功
3. CommitRecords(record) → 告訴 Broker「我已處理到 100」
4. 重啟後，從 101 繼續，✅ 不遺漏
```

### 護城河 2：指數退避重試 (Exponential Backoff)

```go
fetches.EachRecord(func(record *kgo.Record) {
    backoff := 100 * time.Millisecond  // 初始等待 100ms
    for {
        if ctx.Err() != nil { return } // 優先響應關機訊號

        if err := handler(ctx, record.Key, record.Value); err != nil {
            logger.Error("訊息處理失敗，等待重試",
                zap.Duration("backoff", backoff), ...)

            // ⭐ 使用 select 而非 time.Sleep，確保可被 ctx.Done 中斷
            select {
            case <-time.After(backoff):
                backoff *= 2                          // 每次失敗，等待時間翻倍
                if backoff > 30*time.Second {
                    backoff = 30 * time.Second        // 最長等 30 秒
                }
            case <-ctx.Done():
                return  // 收到關機訊號，立即退出
            }
            continue
        }

        // ✅ 處理成功後才 Commit
        c.client.CommitRecords(ctx, record)
        break
    }
})
```

**為什麼用指數退避而非固定 1 秒？**
```
DB 剛掛掉，10 台 Consumer 每秒都打一次 → 雪上加霜，讓 DB 更難恢復
DB 剛恢復，指數退避讓恢復期有更多喘息空間，系統自然慢慢回穩
```

### 護城河 3：優雅關機與 WaitGroup

```go
// consumer.go
type Consumer struct {
    client  *kgo.Client
    wg      sync.WaitGroup  // ← 追蹤 goroutine
}

func (c *Consumer) Start(ctx context.Context, handler HandlerFunc) {
    c.wg.Add(1)
    go func() {
        defer c.wg.Done()
        defer c.client.Close()
        // ... 主迴圈
    }()
}

func (c *Consumer) Wait() {
    c.wg.Wait()  // 阻塞直到 goroutine 完全退出
}

// cmd/server/main.go 的關機序列
cancelConsumers()  // 1. 通知所有 Consumer 停止
// 2. 等待 Consumer 完整結束（最多 10 秒）
shutdownDone := make(chan struct{})
go func() {
    matchConsumer.Wait()
    settleConsumer.Wait()
    close(shutdownDone)
}()
select {
case <-shutdownDone:
    // ✅ Consumer 正常結束
case <-time.After(10 * time.Second):
    // ⚠️ 超時，強制繼續（避免永遠卡住）
}
kafkaProducer.Close()  // 3. 最後才關閉 Producer（確保 in-flight 事件不丟失）
```

---

## 五、Matching Consumer：撮合的事件處理器

```go
// internal/core/matching_consumer.go

func (s *ExchangeServiceImpl) HandleMatchingEvent(ctx context.Context, key, value []byte) error {
    // 第一步：先解碼 EventType 做路由，避免全量解析（效能優化）
    var envelope struct {
        EventType EventType `json:"event_type"`
    }
    json.Unmarshal(value, &envelope)

    switch envelope.EventType {
    case EventOrderPlaced:
        // → 觸發記憶體撮合，發布 SettlementRequestedEvent
        return s.handleOrderPlaced(ctx, &event)

    case EventOrderCancelRequested:
        // → 從 In-Memory Engine 移除掛單
        return s.handleOrderCancelRequested(ctx, &event)

    default:
        // ⚠️ 未知事件：記錄警告後 Commit（不能讓未知訊息卡住整個 Partition！）
        logger.Warn("收到未知 EventType，跳過", ...)
        return nil
    }
}
```

### 撮合後的關鍵細節：發布不失敗

```go
func (s *ExchangeServiceImpl) handleOrderPlaced(ctx context.Context, event *OrderPlacedEvent) error {
    trades := engine.Process(matchOrder)

    // 為什麼這裡要無限重試發布，而不是 return err？
    // 如果 return err，外層 Consumer 會「重試整筆訊息」
    // 但撮合引擎是「記憶體有狀態的」！
    // 重試等於再撮合一次 → 產生重複 Trade！🔴 嚴重 Bug！
    for {
        err := s.eventBus.Publish(ctx, TopicSettlements, event.Symbol, settlementEvent)
        if err == nil { break }
        if ctx.Err() != nil {
            // 收到關機訊號：return err 讓外層不 Commit
            // 重啟後引擎從 DB Hydration 恢復，重新撮合是安全的
            return ctx.Err()
        }
        logger.Warn("發布失敗，重試...")
        time.Sleep(1 * time.Second)
    }
    // ...
}
```

---

## 六、Settlement Consumer：最後一公里與 TOCTOU 防禦

### 雙重冪等性保護

```go
func (s *ExchangeServiceImpl) HandleSettlementEvent(ctx context.Context, key, value []byte) error {
    var event SettlementRequestedEvent
    json.Unmarshal(value, &event)

    // === 外層快速檢查（減少不必要的 TX 開銷）===
    if len(event.Trades) > 0 {
        exists, _ := s.tradeRepo.TradeExistsByID(ctx, event.Trades[0].ID)
        if exists {
            logger.Info("結算事件已處理，跳過") // 快速跳過
            return nil
        }
    } else {
        takerOrder, _ := s.orderRepo.GetOrder(ctx, event.TakerOrderID)
        if takerOrder.Status != StatusNew {
            return nil  // 無成交訂單已被處理
        }
    }

    return s.executeSettlementTx(ctx, &event)
}
```

### TX 內部的二次確認 (解決 TOCTOU)

```go
func (s *ExchangeServiceImpl) executeSettlementTx(ctx context.Context, event *SettlementRequestedEvent) error {
    return s.txManager.ExecTx(ctx, func(ctx context.Context) error {
        // Phase 1: 依 UUID 字典序排序後，FOR UPDATE 鎖定所有訂單
        sort.Slice(allOrderIDs, ...)
        for _, id := range allOrderIDs {
            lockedOrder, _ := s.orderRepo.GetOrderForUpdate(ctx, id)
            lockedOrders[id] = lockedOrder
        }

        takerOrder := lockedOrders[event.TakerOrderID]

        // ⭐ Phase 1.1: TX 內部二次檢查（TOCTOU 防禦）
        // 以下情境：兩台 Server 同時收到相同事件
        // 它們都通過外層快速檢查（都讀到 Status=NEW）
        // 但 FOR UPDATE 保證同一時間只有一個能進入此區段
        if takerOrder.Status != StatusNew {
            logger.Info("TX 內偵測重複事件，跳過（TOCTOU 保護）", ...)
            return ErrIdempotencySkip  // 讓 ExecTx 優雅結束，不觸發 Rollback 報錯
        }

        // Phase 2-3: 計算並寫入結算結果...
        // ...
    })
}
```

**TOCTOU 攻防圖示：**
```
Kafka Rebalance 發生，兩台 Worker 同時收到同一筆 settlement-event

Worker A                          Worker B
────────────                      ────────────
外層讀 Order → Status=NEW ✓       外層讀 Order → Status=NEW ✓
↓                                 ↓
ForUpdate 鎖定...                  ForUpdate 等待...（被 A 鎖住）
                                       ↓
TX 內確認 Status=NEW ✓             ↓
進行結算...                             ↓
UpdateOrder: Status=FILLED         Worker A Commit
Commit ✓                          ↓
                                  ForUpdate 拿到鎖
                                  ↓
                                  TX 內確認 Status=FILLED ✗
                                  return ErrIdempotencySkip → NO-OP
                                  ✅ 資金不重複結算！
```

---

## 七、整體事件流程圖

```
┌─────────────────────────────────────────────────────────────────────┐
│                        完整 Kafka 交易流程                           │
└─────────────────────────────────────────────────────────────────────┘

前端 HTTP POST /orders
        │
        ▼
┌──────────────┐
│   API Layer  │ 參數驗證、UserID 注入
└──────┬───────┘
        │
        ▼
┌──────────────────────────────────┐
│  TX1: 鎖定資金 + 建立訂單 (DB)   │ ← PostgreSQL Transaction
│  LockFunds + CreateOrder         │
└──────┬───────────────────────────┘
        │ TX1 Commit 成功
        ▼
┌──────────────────────────────────┐
│  Publish: OrderPlacedEvent       │ → exchange.orders (Kafka)
│  key = "BTC-USD" (Partition Key) │
└──────┬───────────────────────────┘
        │
        │ HTTP 202 Accepted ← 使用者立即收到回應
        │
        ▼ (非同步)
┌──────────────────────────────────┐
│  matching-engine Consumer         │
│  HandleMatchingEvent              │
│  ├─ engine.Process() 記憶體撮合   │
│  └─ Publish: SettlementRequested │ → exchange.settlements (Kafka)
└──────────────────────────────────┘
        │
        ▼ (非同步)
┌──────────────────────────────────┐
│  settlement-engine Consumer       │
│  HandleSettlementEvent            │
│  ├─ 外層冪等性快速檢查            │
│  └─ TX2: 雙重確認 + 結算 (DB)    │ ← PostgreSQL Transaction
│       ├─ FOR UPDATE 鎖所有訂單   │
│       ├─ TX 內 TOCTOU 二次確認  │
│       ├─ UpdateOrder × N        │
│       ├─ CreateTrade × M        │
│       └─ UnlockFunds / Update   │
└──────┬───────────────────────────┘
        │
        ▼
┌──────────────────────────────────┐
│  WebSocket Broadcast              │
│  OnOrderUpdate + OnOrderBookUpdate│
└──────────────────────────────────┘
        │
        ▼
前端即時收到成交通知 ✅
```

---

## 八、Graceful Degradation：Kafka 掛掉怎麼辦？

本系統保留了「同步 Fallback 模式」，當 Kafka 不可用時自動降級：

```go
// PlaceOrder 內
if s.eventBus != nil {
    // ✅ Kafka 模式：發布事件，非同步撮合
    s.eventBus.Publish(ctx, TopicOrders, order.Symbol, event)
    return nil
}

// ⬇️ Kafka 不可用（eventBus == nil）：退回同步模式
matchOrder := s.convertToMatchingOrder(order)
trades := engine.Process(matchOrder)
// ... 直接執行 TX2 結算
```

```go
// main.go 啟動時
producer, err := kafka.NewProducer(kafkaCfg)
if err != nil {
    logger.Warn("Kafka 連線失敗，系統以同步撮合模式運作")
    // eventBus 保持 nil → 自動使用 Fallback
} else {
    eventBus = producer  // Kafka 可用，使用非同步模式
}
```

---

## 九、面試重點整理

| 問題 | 本系統的答案 |
|------|------------|
| **為何用 Partition Key = Symbol？** | 保證同一交易對的訂單/撤單嚴格有序，不同交易對並行 |
| **At-Least-Once 如何處理重複消費？** | Manual Commit + 雙重冪等性（TX 外快速跳過 + TX 內 FOR UPDATE 二次確認） |
| **撮合後 Publish 失敗如何處理？** | 無限重試，絕不 return err（避免 Consumer 重試 = 重複撮合的雙重風險） |
| **Consumer 崩潰如何恢復？** | DisableAutoCommit → 未 Commit 的 Offset 重新消費；引擎 Hydration 保證記憶體一致 |
| **如何保證關機不丟訊息？** | WaitGroup 等待 Consumer 完成 → 再關閉 Producer |
| **Kafka 不可用時系統怎麼辦？** | 自動降級到同步撮合模式（eventBus == nil 判斷） |

---

👉 **下一篇**：[架構設計模式（六角架構、UUID v7、Decimal 精度）](08_architecture_patterns.md) | **導覽**：[文件總覽 README](README.md)

---

👉 **下一篇**：[架構設計模式（六角架構、UUID v7、Decimal 精度）](08_architecture_patterns.md) | **導覽**：[文件總覽 README](README.md)
