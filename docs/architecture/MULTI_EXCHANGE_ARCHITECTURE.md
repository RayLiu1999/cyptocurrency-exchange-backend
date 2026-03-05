# 多交易所策略平台：微服務擴展架構設計

## 1. 整體架構概念
本架構旨在從原本的「單一交易所模擬器」擴展為一個「具備多交易所行情接入、策略回測與實時監控」的綜合交易系統。核心理念是將**行情獲取**、**撮合邏輯**、**策略判斷**完全解耦。

## 2. 核心組件說明

### 2.1 外部對接層 (External Adapter - CCXT Inspired)
為了適應多家交易所，我們引入標準化介面：
*   **Adapter Pattern**：針對 Binance, OKX 等開發各自的 Adapter，但對外暴露統一的 `FetchOrders()`, `SubscribeKLines()` 介面。
*   **Rate Limiting**：在 Adapter 層實作各交易所的 API 限流與權重管理。

### 2.2 行情流水線 (Market Data Ingestion)
數據依時效性分為兩路：
1.  **歷史數據路徑 (Pull)**：透過 CCXT REST API 批量抓取，洗白後存入 **PostgreSQL/TimescaleDB**。用戶回測時從此處讀取。
2.  **即時行情路徑 (Push)**：透過原生 WebSocket (為了極致低延遲) 抓取，發送到內部的 **Event Bus (NATS/Redis)**，驅動監控面板與實時策略。

### 2.3 策略引擎 (Strategy & Backtest Logic)
這是系統的最上層，負責決策：
*   **Backtest Manager**：它是資料庫與撮合引擎的橋樑。它模擬時間流逝，將歷史數據順序推送到撮合引擎中。
*   **Strategy Executor**：獨立微服務。
    *   *Input*: 實時行情事件。
    *   *Output*: 交易報文 (Internal Order Request)。

### 2.4 核心交易服務 (Core Trading System)
即你目前正在開發與壓測的部分：
*   **Order Service**：管理訂單生命週期、成交狀態。
*   **Matching Engine**：純記憶體運作，負責極速撮合。
*   **Account Service**：管理資產、對賬、凍結與解凍。

## 3. 技術選型建議 (中期演進)

| 模組 | 建議技術 | 理由 |
| :--- | :--- | :--- |
| **時序資料庫** | TimescaleDB | 建構在 PostgreSQL 上，處理 K 線與 Ticker 極快。 |
| **消息中間件** | NATS JetStream | 極低延遲且支援持久化，適合作為內部行情傳遞。 |
| **緩存層** | Redis | 存儲 TWS (Top of Book) 深度與熱點 K 線。 |
| **API 標準** | gRPC | 微服務內部通訊使用，確保高效能與類型安全。 |

## 4. 數據流向圖 (Mermaid)
*(詳見 docs/ARCHITECTURE.md 或專案架構概覽說明)*
