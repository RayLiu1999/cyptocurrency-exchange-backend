## 微服務架構映射

本專案採用**模組化單體儲存庫 (Monorepo)** 架構，所有原始碼集中管理，微服務界線由 `cmd/` 目錄下的各個 `main.go` 進入點劃分。

### 📋 服務載入清單

執行 `docker-compose.test.yml` 時，各微服務的檔案依賴如下：

#### 🏛️ 共用基礎層
所有微服務共同引入：

| 元件 | 位置 | 用途 |
|------|------|------|
| 模型定義 | `internal/core/domain.go` | 所有資料模型 |
| 介面定義 | `internal/core/ports.go` | 依賴注入契約 |
| 日誌系統 | `internal/infrastructure/logger` | 統一日誌管理 |
| 監控指標 | `internal/infrastructure/metrics` | Prometheus 指標 |
| 資料庫實作 | `internal/repository` | 持久化層 |

#### 🛒 訂單服務 (Order Service)
**職責**：訂單接收、處理、結帳  
**效能特徵**：I/O 密集

- **進入點**：`cmd/order-service/main.go`
- **API 層**：`internal/api/*` (Gin 路由、HTTP 請求解析)
- **核心邏輯**：`internal/core/order_service.go`
- **非同步處理**：`internal/infrastructure/outbox/worker.go`
- **結算消費**：`internal/core/settlement_consumer.go` (併行處理資料庫扣款)

#### 🧠 撮合引擎 (Matching Engine)
**職責**：高速訂單匹配運算  
**效能特徵**：CPU 密集、零資料庫依賴

- **進入點**：`cmd/matching-engine/main.go`
- **核心模組**：`internal/core/matching/*` (B-Tree 排序、記憶體計算)
- **消息層**：`internal/core/matching_consumer.go` (Kafka 主題: `orders` → `settlements`)

#### 📈 市場數據服務 (Market Data Service)
**職責**：即時行情推播、WebSocket 連線管理  
**效能特徵**：網路頻寬密集

- **進入點**：`cmd/market-data-service/main.go`
- **WebSocket 層**：`internal/api/websocket*`
- **快取層**：Redis 操作、K 線圖與深度圖生成

#### 🚪 API 網關 (Gateway)
**職責**：請求驗證、速率限制、請求轉發  
**效能特徵**：吞吐量關鍵

- **進入點**：`cmd/gateway/main.go`
- **安全檢查**：認證、授權
- **路由轉發**：Proxy 至 `order-service`

