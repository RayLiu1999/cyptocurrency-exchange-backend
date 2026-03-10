# 專案架構文件 (Architecture Document)

> **本文件為架構的唯一真相來源。** 記錄從當前單體架構到最終 CCXT 多交易所平台的完整演進路徑。

---

## 0. 專案目標與演進路線

```
現在                        近期目標                    長期目標
─────────────────────────────────────────────────────────────────────
單體 Go Server          → Redis + Kafka 非同步     → CCXT 多交易所
+ PostgreSQL            → ECS 微服務壓力測試       → 策略回測平台
(本地穩定運行)          → 學習 AWS 各項功能         → TimescaleDB + NATS
```

### 三大階段說明

| 階段 | 狀態 | 核心目標 |
|------|------|----------|
| **Stage 1：現行單體** | ✅ 完成 | 撮合引擎 MVP，本地可用 |
| **Stage 2：非同步微服務 + ECS** | 🔄 進行中 | 加 Redis/Kafka，拆微服務，上 ECS 壓測，學 AWS |
| **Stage 3：CCXT 多交易所平台** | 📋 規劃 | 接入真實行情，實作策略回測 |

---

## 1. 當前架構：單體 (Current State - Stage 1 ✅)

```mermaid
graph TD
    CLIENT["用戶 (瀏覽器 / curl)"]
    subgraph "本機 / EC2"
        API["Go Server (Gin)\ncmd/server"]
        MATCH["Matching Engine\n(In-Memory OrderBook)"]
        PG[("PostgreSQL")]
        WS["WebSocket\n即時推送"]
    end

    CLIENT -->|HTTP REST| API
    CLIENT <-->|ws://| WS
    API --> MATCH
    MATCH -->|成交結果| PG
    API --> PG
    MATCH -->|廣播| WS
```

**現行技術棧：**
- **Web Framework**: Gin
- **撮合引擎**: 全記憶體 (In-Memory)，Price-Time Priority 演算法
- **資料庫**: PostgreSQL（訂單、帳戶、成交記錄）
- **即時推送**: WebSocket (gorilla/websocket)
- **日誌**: Uber Zap (結構化)

**目錄結構（現行）：**
```
backend/
├── cmd/server/           # 單體 API 服務進入點
├── cmd/simulator/        # 壓測行情模擬器
├── internal/
│   ├── api/              # HTTP/WS Handler (Gin)
│   ├── core/             # 領域邏輯：service.go, domain.go, ports.go
│   ├── core/matching/    # 撮合引擎核心：engine.go, orderbook.go
│   ├── repository/       # PostgreSQL 存取層
│   ├── simulator/        # 模擬下單 Service
│   └── infrastructure/logger/
├── sql/                  # schema.sql, seed.sql
└── backups/infra/terraform/  # ECS, RDS, ALB IaC
```

---

## 2. 核心設計模式：六角架構 (Ports & Adapters)

核心邏輯 (`internal/core/`) 完全不認識 PostgreSQL、Redis 或任何外部框架。  
它只依賴自己定義的介面 (Ports)，外部實作插入進來 (Adapters)。

```
┌────────────────────────────────────────────────────────┐
│         Presentation Layer  (internal/api/)            │
│   Gin Handlers • WebSocket • Request/Response 轉換     │
└──────────────────────┬─────────────────────────────────┘
                       │ 呼叫 ExchangeService 介面
                       ▼
┌────────────────────────────────────────────────────────┐
│         Application Layer  (internal/core/)            │
│   domain.go • service.go • ports.go (interfaces)       │
│   Matching Engine (OrderBook, In-Memory)               │
└──────────────────────┬─────────────────────────────────┘
                       │ 透過 Interface 解耦（依賴反轉）
                       ▼
┌────────────────────────────────────────────────────────┐
│   Infrastructure Layer  (repository/ + infrastructure/)│
│   postgres.go → (未來) redis.go • kafka.go • ccxt.go   │
└────────────────────────────────────────────────────────┘
```

**好處**：要把 PostgreSQL 換成 TimescaleDB，或把 REST 換成 gRPC，只換 Adapter，核心邏輯完全不動。

---

## 3. 目標架構：非同步微服務 + ECS (Stage 2 🔄)

**目的**：加入 Redis 快取 + Kafka 削峰，再拆微服務部署到 AWS ECS 做壓力測試，學習 AWS 各項功能。

### 3.1 加入 Redis + Kafka 後的非同步架構

```mermaid
graph TD
    CLIENT["用戶 / k6 壓測"]

    subgraph "AWS ECS Cluster"
        ALB["ALB (負載均衡)"]

        subgraph "API Tasks (水平擴展)"
            API1["API Task 1"]
            API2["API Task 2"]
        end

        subgraph "Worker Tasks"
            WORKER["Matching Worker\n(Kafka Consumer)"]
        end

        KAFKA[("Kafka\nTopic: orders")]
        REDIS[("Redis\n訂單簿快取")]
        PG[("RDS PostgreSQL\n訂單/帳戶/成交")]
    end

    CLIENT -->|HTTPS| ALB
    ALB --> API1 & API2

    API1 & API2 -->|1. Produce| KAFKA
    API1 & API2 -->|回傳 202 Accepted| CLIENT

    KAFKA -->|2. Consume| WORKER
    WORKER -->|撮合 + 寫入| PG
    WORKER -->|更新快取| REDIS
    REDIS -->|Cache Hit / Pub-Sub| API1 & API2
```

### 3.2 Redis 的用途

| 用途 | Key Pattern | TTL |
|------|-------------|-----|
| 訂單簿快取 | `orderbook:{symbol}` | 500ms |
| K 線快取 | `kline:{symbol}:{interval}` | 1m |
| Session / Rate Limit | `ratelimit:{user_id}` | 1s |

### 3.3 Kafka 的用途（削峰填谷）

```
同步（現在）：  HTTP → 鎖資金(DB) → 撮合 → 更新(DB) → 回傳  ← 高延遲
非同步（目標）：HTTP → Produce 到 Kafka → 回傳 202        ← < 5ms
                     Worker 從 Kafka 消費 → 撮合 → DB 更新
```

**Topic 設計：**
- `orders.new`：新訂單（API → Worker）
- `orders.result`：成交結果（Worker → API / WS）
- `market.kline`：K 線更新事件

### 3.4 微服務拆分

| 服務 | `cmd/` 入口 | 職責 |
|------|------------|------|
| **API Gateway** | `cmd/gateway` | 驗證/限流/路由（規劃中） |
| **Order Service** | `cmd/order-service` | 下單/撤單/訂單生命週期 |
| **Matching Engine** | `cmd/matching-engine` | 純記憶體撮合，單實例 |
| **Monolith** | `cmd/server` | 開發用整合服務（向後保留） |

### 3.5 ECS 壓測目標

| 指標 | 目標 |
|------|------|
| TPS（每秒下單數） | > 1000 TPS |
| P99 延遲 | < 50ms |
| 服務可用性 | > 99.9% |
| 壓測工具 | k6 |

**要學習的 AWS 服務：**
- **ECS Fargate**：無伺服器容器，Auto Scaling
- **ALB**：路徑路由、Health Check
- **RDS**：託管 PostgreSQL，快照備份
- **ElastiCache**：託管 Redis
- **CloudWatch**：Metrics、Logs、Alarm
- **ECR**：Docker Image 倉庫
- **SSM Parameter Store / Secrets Manager**：密鑰管理

---

## 4. 最終目標：CCXT 多交易所平台 (Stage 3 📋)

**目的**：壓測完成、學完 AWS 後，保留撮合引擎核心，轉型為接入真實行情的策略回測平台。

### 4.0 全系統架構圖（最終態）

```mermaid
graph TB
    subgraph "Frontend Layer (React/Vue)"
        UI[Trading Dashboard / Strategy Editor]
    end

    subgraph "API Gateway Layer (Gin)"
        AG[API Gateway]
        WSG[WebSocket Gateway]
    end

    subgraph "Core Microservices (Go)"
        OS[Order Service]
        ME[Matching Engine]
        AS[Account Service]
    end

    subgraph "Strategy & Backtest Engine"
        SE[Strategy Executor]
        BE[Backtest Engine]
    end

    subgraph "External Adapter Layer (CCXT / Native)"
        subgraph "Market Data Collector"
            CCXT[CCXT Provider Interface]
            BS[Binance Sync]
            OKS[OKX Sync]
            BYS[Bybit Sync]
        end

        subgraph "Ingestion Pipeline"
            NB[NATS / Redis PubSub]
        end
    end

    subgraph "Data Layer"
        PG[(PostgreSQL / TimescaleDB)]
        RD[(Redis Cache)]
    end

    %% 使用者流程
    UI -->|REST/WS| AG
    AG --> OS
    OS --> NB
    NB --> ME
    ME --> NB
    NB --> WSG
    WSG -->|Real-time Data| UI

    %% CCXT 歷史數據流
    CCXT -->|History/KLines| BS
    CCXT -->|History/KLines| OKS
    CCXT -->|History/KLines| BYS
    BS & OKS & BYS -->|Batch Write| PG

    %% 策略 / 回測流程
    PG -->|History Data| BE
    NB -->|Real-time Signal| SE
    SE -->|Generated Order| AG
    BE -->|Mock Execution| ME

    %% 資料庫連結
    OS & ME & AS --> PG
    ME & SE --> RD
```

**關鍵設計：**

```go
// internal/exchange/provider.go
type ExchangeProvider interface {
    FetchKLines(ctx context.Context, symbol string, interval string, since time.Time) ([]KLine, error)
    SubscribeTicker(ctx context.Context, symbol string) (<-chan Ticker, error)
}
```

不管底層對接的是 Binance 或 OKX，上層策略引擎只看 `KLine` 與 `Ticker` 的統一介面。

**技術選型：**

| 組件 | 技術 | 理由 |
|------|------|------|
| 時序資料庫 | TimescaleDB | PostgreSQL 擴展，K 線查詢快 10x |
| 訊息中間件 | NATS JetStream | 極低延遲，適合內部行情傳遞 |
| 回測數據校驗 | Gap Detection | 自動補齊斷線期間的缺失 K 線 |
| 效能分析 | Sharpe Ratio / MDD | 回測報表必備指標 |

---

## 5. 文件索引
