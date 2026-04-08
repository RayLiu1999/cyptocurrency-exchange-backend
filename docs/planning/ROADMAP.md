# 專案藍圖 (Project Roadmap)

這份文件記錄了目前專案的發展階段與短期、長期的可執行目標。
核心架構為：**Redis -> Kafka -> Microservice**

---

## Phase 1: 本地與 ECS 壓測 (Current)
**目標：確保目前的非同步微服務架構在壓力下不僅能正常運作，且具備可容錯性。**
- [ ] **本地測試 (Local Testing)**: 使用 k6 或其他工具在本地進行高併發壓測，觀察 Redis 緩存與 Kafka Message Queue 的吞吐量與延遲。
- [ ] **ECS 測試部署**: 將服務短期部署至 AWS ECS 進行真實環境的壓力測試。此環境主要用於驗證雲端網路、IAM 權限與基礎設施效能。
- [ ] **ECS 重整任務執行**: 依照 [deploy/docs/04-ECS-MICROSERVICES-EXECUTION-CHECKLIST.md](../../deploy/docs/04-ECS-MICROSERVICES-EXECUTION-CHECKLIST.md) 逐步完成 microservice deploy 重建、staging 驗證與 production-ready backlog 收斂。
- [ ] **瓶頸分析與調優**: 找出系統的效能瓶頸（如 Database 連線數、Kafka 消費速度）並進行調整。

## Phase 2: VPS K3s 基礎部署 (Mid-term)
**目標：將系統從短暫的 ECS 測試環境轉移至長期穩定、低成本的 VPS K3s 叢集。**
- [ ] **K3s 集群建置**: 在 VPS 上搭建並配置單節點或多節點的 K3s。
- [ ] **基礎設施遷移**: 透過 Helm/Manifest 將 Redis, Kafka, PostgreSQL 等有狀態服務部署或連接至 K3s。
- [ ] **微服務遷移**: 將交易引擎、API Gateway 等無狀態服務以 Deployment 形式運行於 K3s。
- [ ] **CI/CD 建置**: 設定自動化部署流程，當程式碼推進時自動建置 Image 並更新至 K3s 集群。

## Phase 3: 核心功能擴充 - CCXT (Stage 3)
**目標：引入真實的交易所資料與交易功能，打造完整的量化/聚合交易系統。**
- [ ] **CCXT 整合**: 在後端整合 [CCXT](https://docs.ccxt.com/) 庫，支援多間主流交易所（如 Binance, OKX 等）的 API。
- [ ] **資料收集模組 (Data Collector)**: 透過 WebSockets 或 REST 獲取即時 K 線 (OHLCV)、OrderBook 等市場數據。
- [ ] **訂單路由 (Order Routing)**: 實作下單模組，允許使用者跨交易所執行買賣單，並妥善處理 API Rate Limits。

## Phase 4: VPS K3s 長期維運 (Long-term)
**目標：完善系統的可觀測性與穩定性，進入生產級別標準。**
- [ ] **可觀測性建置 (Observability)**: 部署 Prometheus, Grafana, Alertmanager 監控 K3s 節點與各微服務健康度。
- [ ] **日誌聚合**: 導入 Loki 或 ELK stack 蒐集並集中管理各容器日誌。
- [ ] **高可用性與災難復原**: 設定資料庫定期備份機制、確認 K3s 集群的容錯轉移能力。

---

## 相關參考檔案
- [ARCHITECTURE.md](../architecture/ARCHITECTURE.md): 目前的微服務架構與拓樸。
- [STRATEGY_BACKTEST_PLATFORM_PLAN.md](./STRATEGY_BACKTEST_PLATFORM_PLAN.md): 未來量化回測平台的規劃。
