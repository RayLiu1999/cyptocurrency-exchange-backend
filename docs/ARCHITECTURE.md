# 專案架構說明

## 目錄結構與用途

```
exchange/
├── .github/                    # GitHub 相關設定
│   └── copilot-instructions.md # Copilot 指令規範
├── api/                        # (預留) API 定義檔案，如 Protobuf, OpenAPI spec
├── cmd/                        # 應用程式入口點 (Entry Points)
│   ├── server/                 # 主要的 HTTP Server (Phase 1 單體架構使用)
│   │   └── main.go            # 啟動伺服器
│   ├── gateway/                # (預留 Phase 2) API Gateway
│   ├── matching-engine/        # (預留 Phase 2) 撮合引擎微服務
│   └── order-service/          # (預留 Phase 2) 訂單服務微服務
├── internal/                   # 私有應用程式碼 (不可被外部 import)
│   ├── core/                   # 核心業務邏輯層 (Domain Layer)
│   │   ├── domain.go          # 領域模型 (Order, Account, User 等)
│   │   ├── ports.go           # 介面定義 (Repository, Service interfaces)
│   │   └── service.go         # 業務邏輯實作 (下單、撮合等)
│   ├── repository/             # 資料存取層 (Persistence Layer)
│   │   └── postgres.go        # PostgreSQL 實作
│   ├── api/                    # HTTP API 層 (Presentation Layer)
│   │   └── handlers.go        # Gin HTTP Handlers
│   └── infrastructure/         # (預留) 基礎設施層 (Kafka, Redis 等)
├── sql/                        # 資料庫相關檔案
│   └── schema.sql             # 資料庫 Schema 定義
├── deploy/                     # (預留) 部署相關檔案 (Kubernetes manifests, Helm charts)
├── terraform/                  # (預留) AWS 基礎設施即程式碼 (IaC)
├── docs/                       # 專案文件
│   └── PROJECT_PLAN.md        # 專案規劃書
├── go.mod                      # Go Module 定義
├── go.sum                      # Go 依賴鎖定檔
└── README.md                   # 專案說明

```

## 架構模式：分層架構 + 六角架構 (Layered + Hexagonal/Ports & Adapters)

### 目前採用的架構模式

本專案採用 **分層架構 (Layered Architecture)** 結合 **六角架構 (Hexagonal Architecture / Ports & Adapters)** 的設計理念：

```
┌─────────────────────────────────────────────────────────────┐
│                     Presentation Layer                      │
│                    (internal/api/)                          │
│  - HTTP Handlers (Gin)                                      │
│  - Request/Response 轉換                                     │
└────────────────┬────────────────────────────────────────────┘
                 │ 呼叫
                 ▼
┌─────────────────────────────────────────────────────────────┐
│                     Application Layer                       │
│                    (internal/core/)                         │
│  - Domain Models (domain.go)                                │
│  - Business Logic (service.go)                              │
│  - Ports (Interfaces) (ports.go)                            │
└────────────────┬────────────────────────────────────────────┘
                 │ 透過 Interface 解耦
                 ▼
┌─────────────────────────────────────────────────────────────┐
│                   Infrastructure Layer                      │
│              (internal/repository/, infrastructure/)        │
│  - PostgreSQL Repository (postgres.go)                      │
│  - (未來) Redis, Kafka Adapters                             │
└─────────────────────────────────────────────────────────────┘
```

### 各層職責說明

#### 1. **Presentation Layer (API 層)**
- **位置**：`internal/api/`
- **職責**：
  - 接收 HTTP 請求
  - 驗證輸入格式 (JSON binding)
  - 呼叫 Application Layer 的 Service
  - 將結果轉換為 HTTP Response
- **特點**：依賴 `core.ExchangeService` 介面，不直接依賴具體實作

#### 2. **Application Layer (核心業務層)**
- **位置**：`internal/core/`
- **職責**：
  - **domain.go**：定義業務實體 (User, Order, Account)
  - **ports.go**：定義介面 (Ports)，描述對外依賴
    - `OrderRepository`：訂單資料存取介面
    - `AccountRepository`：帳戶資料存取介面
    - `ExchangeService`：業務邏輯介面
  - **service.go**：實作業務邏輯 (下單、餘額檢查、鎖定資金)
- **特點**：
  - **不依賴具體的 Database 或 Framework**
  - 只依賴介面 (Interface)，符合依賴反轉原則 (DIP)
  - 可獨立進行單元測試 (Mock Repository)

#### 3. **Infrastructure Layer (基礎設施層)**
- **位置**：`internal/repository/`, `internal/infrastructure/`
- **職責**：
  - 實作 `core/ports.go` 定義的介面
  - **postgres.go**：實作 PostgreSQL 的 CRUD 操作
  - (未來) Kafka Producer/Consumer, Redis Cache
- **特點**：可替換性高，例如可以輕鬆換成 MySQL 或 MongoDB

#### 4. **Entry Point (啟動層)**
- **位置**：`cmd/server/main.go`
- **職責**：
  - 初始化所有依賴 (DB Connection, Repositories, Services)
  - 依賴注入 (Dependency Injection)
  - 啟動 HTTP Server
- **特點**：這是唯一一個「組裝」所有元件的地方

---

## 架構合理性評估

### ✅ 優點 (符合最佳實踐)

1. **清晰的分層**：
   - Presentation, Application, Infrastructure 三層分離
   - 單一職責原則 (SRP)：每層只負責自己的事

2. **依賴反轉 (Dependency Inversion)**：
   - `internal/core/` 定義介面，`internal/repository/` 實作介面
   - 核心業務不依賴具體技術 (符合 Clean Architecture)

3. **可測試性**：
   - 可以輕鬆 Mock `Repository` 來測試 `Service`
   - 業務邏輯與 DB 完全解耦

4. **擴展性**：
   - 預留了微服務架構的目錄 (gateway, matching-engine)
   - 可以逐步從單體演進到微服務

### ⚠️ 目前可改善之處

1. **缺少 Makefile**：
   - 建議：加入 `make build`, `make test`, `make db-migrate` 等指令

2. **缺少 .gitignore**：
   - 目前 `server` 執行檔沒有被忽略
   - 建議：加入 `.gitignore` 排除 build artifacts

3. **缺少設定檔管理**：
   - 目前 DB URL 寫死在 `main.go`
   - 建議：使用 `.env` 或 `config.yaml` (可用 Viper 庫)

4. **缺少 Logging**：
   - 目前只有簡單的 `log.Println`
   - 建議：使用結構化 Logging (如 `zerolog` 或 `zap`)

5. **缺少 Middleware**：
   - 目前沒有身分驗證 (Authentication) 或請求記錄 (Request Logging)
   - 建議：Phase 1.5 加入 JWT 驗證

6. **main.go 在根目錄**：
   - 根目錄的 `main.go` 似乎是測試用，應該刪除或移到 cmd 下

---

## 資料流範例：下單流程

```
1. HTTP Request (POST /orders)
   ↓
2. Gin Handler (internal/api/handlers.go)
   - 驗證 JSON 格式
   - 解析 userID, symbol, price, quantity
   ↓
3. ExchangeService.PlaceOrder() (internal/core/service.go)
   - 檢查訂單有效性
   - 呼叫 AccountRepository.LockFunds() 鎖定資金
   - 呼叫 OrderRepository.CreateOrder() 建立訂單
   - (未來) 呼叫 MatchingEngine 撮合
   ↓
4. PostgresRepository (internal/repository/postgres.go)
   - 執行 SQL Transaction
   - UPDATE accounts SET balance = balance - amount, locked = locked + amount
   - INSERT INTO orders (...)
   ↓
5. 回傳結果給 Handler → HTTP Response
```

---

## Phase 1 vs Phase 2 架構差異

| 項目 | Phase 1 (目前單體) | Phase 2 (微服務) |
|------|-------------------|-----------------|
| **Entry Point** | `cmd/server/main.go` | 多個獨立服務 (gateway, order-service, matching-engine) |
| **通訊方式** | 函式呼叫 | gRPC / Kafka |
| **資料庫** | 單一 PostgreSQL | 可能拆分 (Order DB, Wallet DB) |
| **部署** | 單一 Binary | Docker Compose / K8s |

---

## 建議：下一步優化

1. **加入 Docker Compose**：快速啟動開發環境 (Postgres + Redis)
2. **加入 Migration Tool**：使用 `golang-migrate` 或 `goose` 管理 DB Schema
3. **加入單元測試**：為 `service.go` 寫測試
4. **加入 API 文檔**：使用 Swagger/OpenAPI

---

## 總結

**目前架構是合理且符合業界最佳實踐的。** 它採用了：
- ✅ 分層架構 (易於理解與維護)
- ✅ 依賴反轉 (可測試、可擴展)
- ✅ 六角架構 (核心業務獨立於技術細節)

唯一需要補充的是一些「工程化配套」(Makefile, .gitignore, 設定檔管理)，這些在 Phase 1.5 完善即可。
