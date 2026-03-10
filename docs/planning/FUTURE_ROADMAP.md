# 演進路線圖 (Roadmap)

> 記錄從現在到最終目標的具體行動計畫。詳細架構設計請見 [ARCHITECTURE.md](../architecture/ARCHITECTURE.md)。

---

## Stage 1：現行單體（已完成 ✅）

- [x] Go + Gin 單體 API Server
- [x] In-Memory 撮合引擎（Price-Time Priority）
- [x] PostgreSQL 訂單 / 帳戶 / 成交記錄
- [x] WebSocket 即時推送
- [x] Simulator 壓測模擬器 (`cmd/simulator`)
- [ ] 整理 IaC 目錄：從 `backups/` 移至 `infra/terraform` 並導入 `ecspresso`

---

## Stage 2：非同步微服務 + ECS 壓測（進行中 🔄）

> **開發策略**：採功能分立制，每個分支 (`feat/*`) 必須通過獨立組件測試後才合併至 `main`。

### 階段 A：Redis 快取優化 (`feat/redis-cache`)
- [ ] **獨立組件**：Docker Compose 加入 Redis 鏡像。
- [ ] **實作機制**：核心 Service 導入「Cache Aside」模式（先讀 Redis，未中才讀 DB）。
- [ ] **測試目標**：`GET /orderbook` 性能提升，驗證 Redis 斷線時能正確 fallback 至 DB。

### 階段 B：Kafka 訊息隊列 (`feat/kafka-messaging`)
- [ ] **獨立組件**：Docker Compose 加入 Kafka (Kraft mode)。
- [ ] **實作機制**：API 改為非同步下單，訊息寫入 `orders.new` topic。
- [ ] **測試目標**：模擬 DB 慢速寫入，驗證 API 仍能穩定回傳 `202 Accepted`（削峰填谷）。

### 階段 C：微服務拆分與彙整 (`feat/microservices`)
- [ ] **架構遷移**：將單體代碼拆分為 `order-service` 與 `matching-engine` 獨立進程。
- [ ] **通訊機制**：兩服務透過 Kafka 進行非同步溝通。
- [ ] **測試目標**：全系統整合測試，確保微服務化後邏輯與單體一致。

### 階段 D：AWS ECS 雲端部屬 (`feat/aws-ecs-deploy`)
- [ ] **基礎設施部署**：Terraform 建立 RDS, ElastiCache, MSK (或 EC2 Kafka)。
- [ ] **自動化部署**：導入 `ecspresso` 進行 Task Definition 與 Service 更新管理。
- [ ] **測試目標**：執行雲端壓力測試，對 ALB Endpoint 進行大流量壓測。

---

## Stage 3：CCXT 多交易所平台（規劃 📋）

完成 ECS 壓測學習後，保留撮合引擎核心，轉型為多交易所行情平台。

### 3.1 CCXT 適配層

- [ ] 定義 `ExchangeProvider` Interface（`internal/exchange/provider.go`）
- [ ] 實作 Binance Adapter（REST + WebSocket）
- [ ] 實作 OKX Adapter（可擴展至更多交易所）

### 3.2 Market-Sync 服務

- [ ] 定時抓取歷史 K 線（透過 CCXT REST）
- [ ] Gap Detection：自動補齊斷線期間的缺失數據
- [ ] 遷移到 TimescaleDB（Hypertable 優化時序查詢）

### 3.3 策略回測引擎

- [ ] Backtest Engine：用歷史 K 線驅動撮合引擎模擬時間流逝
- [ ] 績效報表：Sharpe Ratio、最大回撤（MDD）、盈虧比
- [ ] Strategy Executor：可插拔的策略微服務（MA、網格等）

### 3.4 即時行情（選用）

- [ ] 透過 CCXT WebSocket 訂閱 Ticker
- [ ] 發布到 NATS JetStream（低延遲內部事件）
- [ ] 驅動前端即時監控面板

