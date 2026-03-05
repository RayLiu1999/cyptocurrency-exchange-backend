# 面試準備：加密貨幣交易所 Side Project 完整指南

> 本文件涵蓋專案所有技術重點、設計決策與常見面試問題的參考答案。請以自己的語言組織回答，不要直接背稿。

---

## 一、專案一句話介紹（30 秒電梯簡報）

> **「我用 Go 語言從零實作了一個高效能的加密貨幣撮合交易所。核心是一個記憶體內的訂單簿撮合引擎，搭配六角架構設計，並針對分散式部署的演進路徑做了完整的技術規劃，最終以 AWS ECS + Terraform 部署上雲。」**

---

## 二、技術堆疊總覽

| 層次 | 技術 | 選型理由 |
|------|------|---------|
| **語言** | Go 1.24+ | 高併發、低延遲、GC 暫停短，天生適合交易系統 |
| **Web 框架** | Gin | 低開銷的 HTTP 路由，高吞吐量 |
| **資料庫** | PostgreSQL + pgx/v5 | ACID 事務保證、`pgxpool` 連線池 |
| **精度計算** | shopspring/decimal | 避免浮點數精度問題 (IEEE 754)，正確計算金融數字 |
| **日誌** | Uber Zap | 結構化 JSON 日誌，異步寫入，適合 ELK/CloudWatch 收集 |
| **WebSocket** | gorilla/websocket | 穩定的 WS 函式庫，Ping/Pong 心跳管理 |
| **測試** | Testify (mock + assert) | TDD 驅動開發，Mock Repository 隔離單元測試 |
| **API 文件** | Swagger (OpenAPI 3.0) | Design-First，前後端溝通規範 |
| **容器化** | Docker + Docker Compose | 本地開發環境一致性 |
| **IaC** | Terraform | AWS 基礎設施版本化管理 |
| **雲端部署** | AWS ECS Fargate + ALB + RDS | 無伺服器容器，自動擴縮容 |

---

## 三、系統架構設計

### 3.1 整體架構（六角架構 / Hexagonal Architecture）

```
┌──────────────────────────────────────────────┐
│           Presentation Layer (API)            │
│         internal/api/handlers.go              │
│   HTTP Handlers → 驗證輸入 → 呼叫 Service    │
└─────────────────┬────────────────────────────┘
                  │ 依賴 Interface（ExchangeService）
                  ▼
┌──────────────────────────────────────────────┐
│          Application / Domain Layer           │
│    internal/core/service.go + domain.go       │
│   業務邏輯 → 撮合引擎 → 資金鎖定/結算         │
└─────────────────┬────────────────────────────┘
                  │ 依賴 Interface（Repository Ports）
                  ▼
┌──────────────────────────────────────────────┐
│         Infrastructure Layer (Repository)     │
│       internal/repository/postgres.go         │
│          PostgreSQL CRUD 實作                 │
└──────────────────────────────────────────────┘
```

**核心設計原則**：
- **依賴反轉 (DIP)**：`core/` 只依賴 Interface (`ports.go`)，不直接依賴 PostgreSQL
- **可測試性**：用 `MockRepository` 可完全獨立測試業務邏輯
- **可替換性**：把 PostgreSQL 換成 MySQL 只需實作 Interface，不改業務邏輯

### 3.2 Directory 結構

```
internal/
├── api/          # HTTP 層 (Gin handlers, WebSocket)
├── core/
│   ├── domain.go # 領域模型 (Order, Account, Trade, KLine)
│   ├── ports.go  # 所有 Interface 定義 (Ports & Adapters)
│   ├── service.go# 業務邏輯實作
│   └── matching/ # 撮合引擎（獨立 package）
├── repository/   # PostgreSQL 實作
├── infrastructure/
│   └── logger/   # Zap 結構化日誌
└── simulator/    # 市場模擬器（自動下單測試）
cmd/
├── server/       # 單體應用入口（Phase 1 生產用）
├── gateway/      # (預留) API Gateway 微服務
├── matching-engine/ # (預留) 獨立撮合引擎微服務
└── order-service/   # (預留) 訂單服務微服務
```

---

## 四、核心技術深度解析

### 4.1 撮合引擎（Matching Engine）—— 最核心的部分

#### 演算法：Price-Time Priority（價格優先 + 時間優先）

```
買入訂單進來 → 找最低賣價 → 價格 ≥ 最低賣價 → 成交（用 Maker 價格）
                          → 價格 < 最低賣價 → 加入買單佇列等待

成交價格 = Maker 訂單的價格（先在訂單簿等待的那一方）
```

#### 訂單簿資料結構

```go
type OrderBook struct {
    symbol string
    bids   []*Order  // 買單：降序排列（最高價在最前）
    asks   []*Order  // 賣單：升序排列（最低價在最前）
}
```

- **BestBid**：`bids[0]`（最高買價）
- **BestAsk**：`asks[0]`（最低賣價）
- 每次加入新訂單後透過 `sort.Slice` 維持排序

#### 全記憶體設計的理由

| 設計選擇 | 理由 |
|---------|------|
| 訂單簿純記憶體，不落地 DB | 撮合需要微秒級回應，DB 讀寫延遲 ms 級無法接受 |
| 成交結果非同步寫 DB | 撮合完成後再持久化，不阻塞主流程 |
| 單一實例（Singleton Engine per Symbol） | 訂單簿是有狀態的，多實例會導致狀態不一致 |

#### 執行緒安全（Thread Safety）

```go
type Engine struct {
    orderBook *OrderBook
    mu        sync.Mutex  // 每個 Symbol 一把鎖
}
```

`EngineManager` 用 `sync.RWMutex` 管理多交易對的 Engine map，並使用 **Double-Check Locking** 避免重複建立 Engine。

#### 支援的訂單類型

| 訂單類型 | 行為 |
|---------|------|
| **Limit Order（限價單）** | 指定價格，價格匹配才成交，否則留在訂單簿 |
| **Market Order（市價單）** | 不指定價格，直接吃掉訂單簿最優價格 |
| **Partial Fill（部分成交）** | Taker 數量 > Maker 時，可連續吃掉多個 Maker |

---

### 4.2 資金管理：Lock → Match → Settle

下單的完整流程（保證資金安全）：

```
1. 鎖定資金（DB 事務）
   BUY  → 鎖定 Quote 貨幣（如 USD）= price × quantity
   SELL → 鎖定 Base 貨幣（如 BTC）= quantity

2. 建立訂單（DB 事務，與步驟1同一事務）

3. 撮合（記憶體操作，不落地 DB）

4. 結算成交（每筆成交）：
   BUY 方：解鎖已用 USD，增加 BTC 餘額
   SELL 方：解鎖 BTC，增加 USD 餘額
   Maker 訂單：同步更新 filled_quantity 與狀態

5. 未成交部分：保持鎖定，等待後續成交
```

**關鍵設計：步驟 1+2 在同一個 DB 事務**，確保「鎖了資金但訂單沒建立」或「訂單建立但沒鎖資金」的極端情況不存在。

---

### 4.3 資料庫設計

#### 核心 Schema

```sql
-- 精度：DECIMAL(20, 8) 支援到小數點後 8 位（符合加密貨幣精度）
-- balance >= 0 與 locked >= 0 的 CHECK constraint，DB 層防線

accounts (
    user_id, currency, balance, locked  -- Available = balance - locked
)
orders (
    symbol, side, type, price, quantity, filled_quantity, status
)
trades (
    maker_order_id, taker_order_id, symbol, price, quantity
)
```

#### 效能索引設計

```sql
-- 查訂單用（用戶歷史、狀態篩選）
CREATE INDEX idx_orders_user_id ON orders(user_id);
CREATE INDEX idx_orders_status ON orders(status);

-- 撮合引擎未來可從 DB 恢復訂單簿時使用
CREATE INDEX idx_orders_symbol_side_price ON orders(symbol, side, price);
```

#### 事務管理（Transaction Propagation）

透過 `context.Context` 傳遞 `pgx.Tx`，實現類似 Spring `@Transactional` 的效果：

```go
func (r *PostgresRepository) ExecTx(ctx context.Context, fn func(ctx context.Context) error) error {
    tx, _ := r.db.Begin(ctx)
    defer tx.Rollback(ctx)  // 確保失敗時 rollback
    ctxWithTx := context.WithValue(ctx, txKey, tx)
    fn(ctxWithTx)           // 下層 Repository 從 context 取出 tx 使用
    return tx.Commit(ctx)
}
```

---

### 4.4 WebSocket 即時推播

架構採用 **事件監聽者模式（Observer Pattern）**：

```
撮合引擎產生 Trade
       ↓
ExchangeService 呼叫 TradeEventListener.OnTrade()
       ↓
WebSocketHandler（實作 TradeEventListener）接收
       ↓
序列化為 JSON，廣播給所有連線的 client
```

**Interface 定義**（解耦）：

```go
type TradeEventListener interface {
    OnTrade(trade *matching.Trade)
}
```

`WebSocketHandler` 實作此 Interface，透過依賴注入在 `main.go` 組裝，`core/service.go` 不需要知道 WebSocket 的存在。

**連線生命週期管理**：
- `Ping/Pong` 心跳（pongWait = 60s，pingPeriod = 54s）
- `WriteDeadline` 防止慢速客戶端阻塞廣播
- 用 channel（register/unregister/broadcast）做 goroutine-safe 的連線管理

---

### 4.5 TDD 測試策略

#### 撮合引擎（engine_test.go）：純單元測試

```
Phase 1: 基本結構（訂單進入正確佇列）
Phase 2: 基本成交（價格匹配 → 成交）
Phase 3: 價格優先（最優價格先成交）
Phase 4: 時間優先（同價位 FIFO）
Phase 5: 部分成交（連續吃掉多個 Maker）
Phase 6: 連續成交（大單 vs 多小單）
Phase 1.5a: 市價單
Phase 1.5b: 多交易對隔離
```

#### 服務層（service_test.go）：Mock Repository

```go
// 用 testify/mock 替換真實 DB
mockOrderRepo := NewMockOrderRepository()
mockAccountRepo := NewMockAccountRepository()

// 設定預期：LockFunds 返回 nil（成功）
mockAccountRepo.On("LockFunds", ...).Return(nil)

// 驗證業務邏輯行為，不需要真實 DB
svc := NewExchangeService(mockOrderRepo, mockAccountRepo, ...)
err := svc.PlaceOrder(ctx, order)
assert.NoError(t, err)
```

**測試隔離原則**：`core/` 的測試不啟動任何網路或資料庫，純邏輯驗證。

---

## 五、架構演進路線圖

| Phase | 架構 | 關鍵技術 | 解決的問題 |
|-------|------|---------|-----------|
| **1（已完成）** | 單體 Monolith | Go + Gin + PostgreSQL + 記憶體撮合引擎 | 核心功能驗證 |
| **2** | 單體 + 微服務拆分準備 | 依賴注入完善 + 介面抽象 | 服務耦合 |
| **3** | 引入 Kafka | Kafka Producer/Consumer | 流量削峰、非同步解耦 |
| **4** | 水平擴展 | AWS ECS + ALB + Auto Scaling | 單機效能瓶頸 |
| **5** | 引入 Redis | Redis Cache + Pub/Sub | DB 讀取壓力、多實例 WS 廣播 |
| **6** | 可觀測性 | Prometheus + Grafana / AWS CloudWatch + X-Ray | 監控告警、追蹤定位 |
| **7（規劃中）** | 完整微服務 | API Gateway + Order Service + Matching Engine + Market Data | 獨立部署、獨立擴縮容 |

---

## 六、AWS 部署架構

```
Internet
   ↓
AWS ALB（Application Load Balancer）
   ↓ HTTPS
ECS Fargate（自動管理底層 EC2）
  ├── API Server Task (Go application)
  └── (未來) 各微服務 Task
   ↓
AWS RDS PostgreSQL（Multi-AZ）
```

**Terraform 管理的資源**：
- `alb.tf`：ALB、Target Group、Listener
- `ecs.tf`：ECS Cluster、Task Definition、Service
- `rds.tf`：RDS Instance、Subnet Group、Security Group
- `ecr.tf`：Container Registry
- `variables.tf`：環境變數（instance type、replica count 等）

---

## 七、面試問題集（Q&A）

### Q1：說說你這個專案最複雜的部分是什麼？

**回答框架**：

> 最複雜的部分是**撮合引擎與資金管理的一致性設計**。
>
> 撮合引擎本身是在記憶體中運作的，為了效能不能每次都去查 DB。但資金的鎖定和結算必須是原子的、持久化的。我的解法是：
>
> 1. **下單時**：在同一個 DB 事務中同時「鎖定資金」+「建立訂單」，確保兩個操作要麼都成功要麼都失敗。
> 2. **撮合時**：純記憶體操作，訂單簿不落地 DB。
> 3. **成交後**：每筆 Trade 再異步寫入 DB，更新雙方帳戶餘額。
>
> 這個設計的挑戰在於步驟 3 如果失敗，記憶體狀態已變但 DB 沒更新。目前的解法是記錄日誌，未來計劃引入 Kafka + Saga Pattern 做補償事務。

---

### Q2：你的撮合引擎為什麼不用 Priority Queue（heap）而是用 slice + sort？

**回答框架**：

> 目前使用 `sort.Slice` 是因為初版以正確性為優先考量，TDD 驅動確保邏輯正確。
>
> `sort.Slice` 的時間複雜度是 O(n log n)，對於訂單簿深度不大的情況（教學/側專案級別）是完全可接受的。
>
> 生產環境的最佳化方案是用 **heap（min-heap for asks, max-heap for bids）**，新增訂單複雜度降至 O(log n)，取最優價是 O(1)。但需要自訂 `container/heap` 介面，增加了程式碼複雜度，因此在驗證核心邏輯正確後才會做這個最佳化。

---

### Q3：Go 的 goroutine 如何保證撮合引擎的線程安全？

**回答框架**：

> 我用了兩層鎖保護：
>
> 1. **Engine 層**：每個交易對的 `Engine` 有一把 `sync.Mutex`，`Process()` 方法進入時加鎖，確保同一時間只有一個 goroutine 在撮合。這符合「撮合引擎是有狀態的單一來源」的設計原則。
>
> 2. **EngineManager 層**：管理多個 Engine 的 map 用 `sync.RWMutex`，讀取時用 `RLock`（允許並發讀），建立新 Engine 時用 `Lock`。同時使用 **Double-Check Locking** 確保在高並發下也不會重複建立同一個 Symbol 的 Engine。

---

### Q4：為什麼選 PostgreSQL 而不是 MySQL？

**回答框架**：

> 幾個考量：
>
> 1. **`DECIMAL(20,8)` 精度**：金融計算需要精確的小數點，PostgreSQL 的 DECIMAL 實作更嚴格。
> 2. **`ON CONFLICT` 語法**：Upsert 操作更簡潔，適合帳戶餘額更新場景。
> 3. **`pgx/v5` Driver**：相比 MySQL driver，pgx 對 PostgreSQL 原生類型支援更好，内置 connection pool（pgxpool）效能更佳。
> 4. **AWS RDS 成熟支援**：PostgreSQL 在 RDS 上有 Multi-AZ、Read Replica 等完整功能。
>
> 如果換成 MySQL，只需要實作同一套 `Repository Interface`，業務邏輯完全不需要改動——這是六角架構的核心優勢。

---

### Q5：你的 WebSocket 如何擴展到多台伺服器？

**回答框架**：

> 目前的設計是單機的：`WebSocketHandler` 在記憶體中維護連線列表，成交事件直接廣播給所在機器的客戶端。
>
> **多實例的問題**：A 機器上的成交，B 機器上的 WebSocket 客戶端收不到。
>
> **解決方案（Phase 5 規劃）**：引入 **Redis Pub/Sub**：
> - 所有機器訂閱同一個 Redis Channel（如 `orderbook:BTCUSDT`）
> - 成交事件發布到 Redis，所有機器的 Subscriber 都能收到
> - 每台機器再把消息廣播給自己負責的 WebSocket 連線
>
> 這樣 WebSocket Server 就變成無狀態的了，可以水平擴展。

---

### Q6：如何做到高精度的金額計算，避免浮點數問題？

**回答框架**：

> 金融系統絕對不能用 `float64`，因為 IEEE 754 的浮點數：`0.1 + 0.2 ≠ 0.3`。
>
> 我使用 [`shopspring/decimal`](https://github.com/shopspring/decimal) 套件，它的底層是用字串+整數模擬的高精度十進位數字，所有加減乘除都是精確的。
>
> 資料庫層用 `DECIMAL(20, 8)`，支援最大 20 位數、小數點後 8 位，符合比特幣（satoshi 精度）和以太坊（wei 精度需要更高，但交易所層面 8 位足夠）的需求。

---

### Q7：你怎麼測試撮合引擎的正確性？

**回答框架**：

> 採用 **TDD（測試驅動開發）** 方法，測試完全覆蓋了撮合邏輯的 7 個 Phase：
>
> ```
> 1. 基本結構（訂單進入正確佇列）
> 2. 基本成交（價格匹配觸發成交）
> 3. 價格優先（最優價優先）
> 4. 時間優先（FIFO，同價位先進先出）
> 5. 部分成交（Taker > Maker 時連續成交）
> 6. 連續成交（一個大單 vs 多個小單）
> 7. 市價單 + 多交易對隔離
> ```
>
> 所有測試都是純單元測試，不依賴任何外部服務，執行速度非常快。這讓我可以放心地重構撮合邏輯，任何回歸都會即時被測試捕獲。

---

### Q8：談談你的 Clean Architecture / 六角架構設計

**回答框架**：

> 核心思想是**依賴只能從外往內**：外層（HTTP Handler、DB Repository）依賴內層（業務邏輯），內層絕不依賴外層的具體實作。
>
> 實作方法是在 `core/ports.go` 定義所有 Interface：
> ```
> OrderRepository, AccountRepository → DB 操作的抽象
> ExchangeService                    → 業務邏輯的抽象
> TradeEventListener                 → 事件通知的抽象
> ```
>
> 好處有三：
> 1. **可測試**：用 Mock 替換真實 DB，純邏輯單元測試
> 2. **可替換**：換資料庫只改 Repository 實作，不動業務邏輯
> 3. **可演進**：後續微服務化時，Service 可以拆拆獨立部署，Interface 不變

---

### Q9：你在這個專案中遇到最難的問題是什麼？

**回答框架**（選自己最有感覺的）：

> **選項 A：事務管理**
> 如何在不破壞 Clean Architecture 的前提下，讓多個 Repository 操作共享同一個 DB 事務？我沒有選擇把 `*pgx.Tx` 直接傳入 Repository 方法（會暴露 DB 實作細節），而是透過 `context.Context` 注入 tx，讓 `getExecutor(ctx)` 方法自動判斷是否在事務中。這樣上層業務邏輯不感知事務的存在。
>
> **選項 B：撮合引擎的設計邊界**
> 撮合引擎的 `matching` package 裡也有自己的 `Order`、`Trade` 類型，而 `core` package 也有。如何劃分邊界，又如何轉換，需要仔細考慮，避免循環 import（Go 不允許）。最終的設計是 `matching` 作為純計算模組，只依賴自己的類型，`service.go` 負責做類型轉換。

---

### Q10：如果系統要支援 1 萬 TPS，你會怎麼設計？

**回答框架**（描述演進計劃）：

> 這就是我 Phase 3~7 規劃要做的事，分幾層解決：
>
> **Layer 1 - 非同步解耦（Kafka）**
> - 下單 API 只做「驗證 + 鎖資金 + 發 Kafka 訊息」，立即回傳 `202 Accepted`
> - 撮合引擎從 Kafka consume 訂單，Processing 不阻塞 API 回應
> - 峰值時 Kafka 做緩衝，避免撮合引擎被淹沒
>
> **Layer 2 - 水平擴展（ECS Auto Scaling）**
> - API 層無狀態，CPU > 70% 自動擴容
> - 撮合引擎有狀態（訂單簿在記憶體），**只能跑一個實例**，高可用用 Hot-Standby
>
> **Layer 3 - 快取（Redis）**
> - 訂單簿快照、K 線、用戶餘額快取進 Redis
> - 減少 DB 讀取壓力，95% 的查詢走 Redis
>
> **Layer 4 - DB 優化**
> - 讀寫分離（RDS Read Replica）
> - 連線池代理（RDS Proxy）解決高並發連線問題

---

### Q11：如何防止重複下單（Idempotency）？

**回答框架**：

> 目前版本尚未實作，但設計方案是：
>
> 1. **Client 端**：每次下單請求附帶一個 UUID `client_order_id`
> 2. **Server 端**：在 `orders` 表加 `UNIQUE(user_id, client_order_id)` 約束
> 3. **重複請求**：DB INSERT 失敗（唯一鍵衝突），Server 回傳原始訂單
>
> 這樣即使網路超時導致 Client 重試，也不會產生重複訂單。

---

### Q12：你的 Swagger 是怎麼管理的？

**回答框架**：

> 採用 **Design-First** 方式，手寫 `swagger.yaml`，再用 `gin-swagger` 渲染 UI。好處是 API 設計可以在實作前就與前端確認，減少後期修改。
>
> API spec 拆分成多個 YAML 檔案管理（`paths/orders.yaml`、`paths/accounts.yaml` 等），透過 `$ref` 引用合併，方便維護。

---

## 八、進階設計問題（Senior 級別）

### Q: Matching Engine 要怎麼做故障恢復（Recovery）？

> **問題**：撮合引擎是純記憶體的，當機後訂單簿會消失。
>
> **解法方案**：
>
> **方案 A（Simple）**：重啟時從 DB 讀取所有 `status = 'NEW' OR 'PARTIALLY_FILLED'` 的訂單，重建訂單簿。缺點是重建期間不能接受新訂單（downtime）。
>
> **方案 B（Event Sourcing）**：所有訂單操作都以事件形式寫入 Kafka（`order_placed`, `order_matched`, `order_cancelled`）。重啟時 replay Kafka 事件重建狀態，可精確恢復到崩潰前的最後一個 committed offset。

---

### Q: 如何防止自成交（Self-Trade Prevention）？

> 交易所通常需要禁止同一個用戶的買單和賣單互相撮合（可能被用於操縱市場）。
>
> 實作位置：`engine.go` 的 `matchBuyOrder` / `matchSellOrder` 中，撮合前檢查 `buyOrder.UserID == bestAsk.UserID`，若相同則跳過該 Maker 訂單，繼續找下一個。目前 TDD TODO list 中標記為「Phase 7: 邊界條件 - 暫緩」。

---

### Q: Decimal 精度如何防止 rounding attack？

> 惡意用戶可能通過極小精度訂單（如 0.00000001 BTC）來測試系統邊界。
>
> 防禦措施：
> 1. 下單前 `order.Price = order.Price.Round(8)` 規格化精度（service.go 已實作）
> 2. 設定最小訂單金額（min notional value）
> 3. 設定 tick size（最小價格單位）和 lot size（最小數量單位）

---

## 九、面試時容易被追問的亮點

| 亮點 | 面試說法 |
|------|---------|
| **TDD** | 「我用 TDD 寫撮合引擎，先寫測試案例，再實作，7 個 Phase 共 X 個測試全部覆蓋」 |
| **六角架構** | 「business logic 完全不依賴 DB，可以 100% 單元測試，不啟動任何基礎設施」 |
| **Context 事務傳播** | 「我用 context.Context 傳遞事務，而不是把 tx 暴露到業務層，保持了 Clean Architecture」 |
| **shopspring/decimal** | 「gold standard 的金融精度計算，避免 IEEE 754 浮點誤差」 |
| **Goroutine + Mutex** | 「撮合引擎的 sync.Mutex + EngineManager 的 sync.RWMutex + Double-Check Locking」 |
| **架構演進路線** | 「Phase 1 是單體，規劃了 7 個演進 Phase 到微服務，每個阶段解決一個具體問題」 |
| **AWS IaC** | 「用 Terraform 管理 ECS + ALB + RDS，基礎設施版本化，可重複部署」 |
| **Zap 結構化日誌** | 「JSON 格式，異步寫入，適合 CloudWatch / ELK 收集，比 log.Println 效能好」 |

---

## 十、履歷撰寫建議

```
加密貨幣撮合交易所 (Go Side Project)                     2025.01 - 2026.03
────────────────────────────────────────────────────────────────────────────
• 以 Go 實作記憶體訂單簿撮合引擎，支援 Price-Time Priority、限價/市價單、部分成交
• 採用六角架構（Ports & Adapters），核心業務邏輯完全解耦於資料庫，透過 Mock Repository 
  實現 100% 單元測試覆蓋，以 TDD 驅動 7 個 Phase 的撮合邏輯開發
• 設計資金鎖定機制（Lock→Match→Settle），使用 DB Transaction + context.Context 傳播
  確保帳戶餘額的原子性與一致性
• 實作 WebSocket 即時成交推播，採用 Observer Pattern + 依賴注入，撮合引擎與通知層完全解耦
• 以 Terraform 管理 AWS 基礎設施（ECS Fargate + ALB + RDS），實現容器化部署與自動擴縮容
• 技術棧：Go, Gin, PostgreSQL, pgxpool, WebSocket, shopspring/decimal, Zap, Swagger,
  Docker, Terraform, AWS ECS/ALB/RDS
```
