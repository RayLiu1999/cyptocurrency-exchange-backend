# Go Exchange System

這是一個用 Go 語言實作的高效能、分散式加密貨幣/股票撮合系統。本專案旨在展示分散式系統設計、微服務架構以及雲端原生部署的最佳實踐。

## 🚀 專案狀態

**Phase 1: 單體 MVP (Monolithic MVP) 已完成** ✅

目前已完成核心撮合引擎與 API 服務的開發，採用 TDD (測試驅動開發) 方法確保核心邏輯的正確性。

### 已實作功能 (Phase 1)
- **核心撮合引擎**: 
  - 支援 **價格優先 + 時間優先 (Price-Time Priority)** 撮合演算法。
  - 支援 **Limit Order (限價單)**。
  - 支援 **部分成交 (Partial Fills)** 與 **連續撮合**。
  - Maker/Taker 價格機制 (Maker 價格成交)。
- **訂單管理系統**:
  - 完整的訂單生命週期 (NEW -> PARTIALLY_FILLED -> FILLED)。
  - 資金鎖定與結算 (Lock -> Match -> Settle)。
- **RESTful API**:
  - `POST /orders`: 下單
  - `GET /orders/:id`: 查詢訂單
  - `GET /orders`: 查詢用戶歷史訂單
- **完整測試覆蓋**:
  - 核心邏輯 (撮合、結算) 100% 測試覆蓋。
  - 包含單元測試與 API 整合測試。

---

## 🛠️ 技術堆疊

- **Language**: Go 1.24+
- **Web Framework**: [Gin](https://github.com/gin-gonic/gin)
- **Database**: PostgreSQL (pgx driver)
- **Decimal Types**: [shopspring/decimal](https://github.com/shopspring/decimal) (高精度金額計算)
- **Testing**: [Testify](https://github.com/stretchr/testify) (Mock & Assertions)
- **Infrastructure**: Docker, Docker Compose

---

## 🏃 如何開始

### 前置需求
- Go 1.24+
- Docker & Docker Compose
- Make

### 快速啟動 (Local Development)

1. **啟動基礎設施 (PostgreSQL)**
   ```bash
   make db-up
   ```

2. **執行 Database Migration**
   ```bash
   make db-migrate
   ```

3. **啟動 API Server**
   ```bash
   make dev
   ```
   Server 將啟動於 `http://localhost:8080`.

### 執行測試

本專案高度重視測試，您可以執行以下指令來驗證系統：

```bash
# 執行所有測試
make test

# 執行測試並查看覆蓋率報告
make test-coverage
```

### 測試 API

您可以使用提供的腳本進行簡單的 API 測試：
```bash
./test-api.sh
```

---

## 📂 專案結構 (Phase 1)

```
.
├── cmd/
│   └── server/       # 應用程式進入點
├── internal/
│   ├── api/          # HTTP Handlers (Gin)
│   ├── core/         # 核心業務邏輯 (Service, Domain Models)
│   ├── matching/     # 撮合引擎邏輯 (OrderBook, Engine)
│   └── repository/   # 資料存取層 (PostgreSQL implementation)
├── sql/              # SQL Migrations
└── docs/             # 專案文檔
```

## 📅 未來規劃 (Roadmap)

- **Phase 1: 單體 MVP (Completed)**
- **Phase 2: 微服務拆分** (Next)
  - 拆分為 Order Service, Matching Service, Account Service
  - 引入 gRPC 進行服務間通訊
- **Phase 3: 分散式架構優化**
  - 引入 Kafka 進行異步撮合與結算
  - 實作 CQRS
- **Phase 4: Kubernetes 部署**
  - 部署至 AWS EKS

詳細規劃請參考 [專案規劃書](docs/PROJECT_PLAN.md)。
