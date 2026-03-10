# 演進路線圖 (Roadmap)

> 記錄從現在到最終目標的具體行動計畫。詳細架構設計請見 [ARCHITECTURE.md](../architecture/ARCHITECTURE.md)。

---

## Stage 1：現行單體（已完成 ✅）

- [x] Go + Gin 單體 API Server
- [x] In-Memory 撮合引擎（Price-Time Priority）
- [x] PostgreSQL 訂單 / 帳戶 / 成交記錄
- [x] WebSocket 即時推送
- [x] Simulator 壓測模擬器 (`cmd/simulator`)
- [x] Terraform IaC（ECS, RDS, ALB）備妥

---

## Stage 2：非同步微服務 + ECS 壓測（進行中 🔄）

### 2.1 加入 Redis + Kafka

- [ ] Redis Cache：訂單簿快取（TTL 500ms），降低 DB 讀取壓力
- [ ] Kafka Producer：`POST /orders` 改為非同步，先 Produce 到 `orders.new` topic
- [ ] Kafka Consumer Worker：從 topic 消費，進行撮合與 DB 寫入
- [ ] 改用 `docker-compose.yml` 一鍵起所有服務（Redis + Kafka + PostgreSQL）

### 2.2 拆分微服務

- [ ] `cmd/matching-engine`：獨立撮合引擎服務，消費 Kafka
- [ ] `cmd/gateway`（選用）：API 路由 + JWT 驗證

### 2.3 部署到 AWS ECS

- [ ] 推送 Image 到 ECR
- [ ] ECS Fargate Task Definition（API + Worker + Matching Engine）
- [ ] ALB 路徑路由設定
- [ ] RDS + ElastiCache（Redis + Kafka on EC2 或 MSK）
- [ ] CloudWatch Metrics / Logs 觀測

### 2.4 壓力測試與學習

- [ ] k6 壓測：目標 1000+ TPS，P99 < 50ms
- [ ] 觀察 ALB、ECS Auto Scaling 行為
- [ ] 紀錄 DB 連線數、CPU、Memory 瓶頸
- [ ] 紀錄結果到 `docs/test-metrics/AWS_STRESS_TEST_METRICS.md`
- [ ] **壓測完畢後，關閉 ECS 節省費用**

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

