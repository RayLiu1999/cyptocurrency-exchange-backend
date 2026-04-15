# 專案架構文件 (Architecture Document)

這份文件概述本專案的核心架構與資料流拓樸。

## 1. 當前架構：微服務與非同步事件驅動
目前的後端系統以 Go 語言開發，並採用基於事件驅動（Event-Driven）的微服務架構，核心資料流向為 **Redis -> Kafka -> Microservice**。

### 1.1 核心元件職責
- **API Gateway / 外部聚合層**: 負責處理使用者的 HTTP 請求與 WebSocket 連線，進行初步防護（Rate Limiting 等）與請求轉發。
- **Redis (Cache & Fast Storage)**: 
  - 承載高頻讀寫，如即時報價 (Ticker/OrderBook)、Session 與非同步任務的短暫狀態儲存。
  - 作為進入 Kafka 之前的快取或防抖動 (Debounce) 中介。
- **Kafka (Message Broker)**:
  - 系統的神經中樞，負責解耦微服務間的通訊。
  - 將核心交易操作（如下單、取消訂單）與後續操作（如資金扣款、撮合、歷史紀錄入庫）非同步化。
  - 確保事件的高吞吐量與 At-least-once (或是 Exactly-once, 若有配置) 的訊息投遞保證。
- **微服務 (Microservices)**: 各自負責獨立業務邏輯，例如：
  - **Trade Engine (交易引擎)**: 接收 Kafka 事件進行訂單撮合，再推回 Kafka 通知結果。
  - **Account Service (帳戶服務)**: 負責資金鎖定與餘額增減。
  - **Market Data Service (行情服務)**: 負責消費歷史撮合結果，聚合成 K 線回傳前端。
- **PostgreSQL (Persistent Storage)**:
  - 負責長期的強一致性資料存儲（使用者資料、歷史訂單明細、錢包流水帳）。

## 2. 部署環境拓樸 (Deployment Environments)
系統演進會依序通過不同的部署階段：

### 2.1 本地與短暫 ECS 壓測環境 (Current)
- **環境設定**: Local Docker Compose 加上臨時啟用的 AWS ECS 叢集。
- **主要用途**: 高併發測試，觀察 Redis 的 Cache Miss/Hit Rate，以及 Kafka Consumer Group 的消費速度是否會造成 Lag。確認整體非同步架構在流量峰值下的可容錯性。

### 2.2 VPS K3s 環境 (Next Stage)
- **環境設定**: 自行託管在 VPS 上的 K3s 輕量級 Kubernetes。
- **主要用途**: 部署完整微服務群集。利用 K8s 提供的自動擴展 (HPA)、配置管理 (ConfigMap) 以及服務發現 (Service Discovery) 來降低維護成本並準備應對長期的上線運作。

## 3. 未來擴充：雙模態與 CCXT 整合
- **雙模態架構 (Dual-Mode)**: 將分為 PAPER（模擬盤）與 LIVE（實盤）兩種執行模式，透過請求路由控制資料來源與實際執行的目的地。
- **CCXT 整合**: 未來將整合 [CCXT](https://docs.ccxt.com/) 核心庫，連接多家如 Binance 等外部交易所。這使得微服務能擴展成「跨交易所聚合平台」，執行資金的跨平台調度與量化交易。
