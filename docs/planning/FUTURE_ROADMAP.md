# 未來演進路線圖：從高效能撮合引擎到全自動交易平台 (Roadmap)

## 0. 當前進度 (Phase 1-4: Infrastructure & Monolith)
* ✅ 單體撮合引擎 MVP 實作
* ✅ AWS ECS Fargate 部署環境 (Terraform)
* ✅ 基礎負載測試指南與模擬器
* 🔄 **進行中**：微服務拆分與雲端高併發壓測

---

## 1. 中期目標 (Phase 5-6: Data Ingestion & CCXT Integration)
*聚焦於外部數據獲取與標準化，為策略引擎供彈藥。*

### 1.1 CCXT 抽象層實作
* **目標**：不論底層是哪家交易所，上層邏輯只看標準化的 `KLine` 與 `Ticker`。
* **關鍵組件**：`ExchangeProvider` Interface。
* **技術選型**：Go 封裝 CCXT (或自定義 REST Client 分別對接主流交易所)。

### 1.2 行情同步微服務 (Market-Sync Service)
* **API 控制**：支援多交易所 (Binance, OKX, Bybit) 的 K 線歷史補全。
* **數據持久化**：使用 PostgreSQL (推薦搭配 TimescaleDB 擴展) 儲存每分鐘 K 線。
* **缺口修復機制**：自動檢測斷線期間的行情缺失並非同步補齊。

---

## 2. 長期目標 (Phase 7-8: Strategy Engine & Backtesting)
*聚焦於交易邏輯的自動化與精準回測。*

### 2.1 歷史回測引擎 (Backtest Engine)
* **模擬撮合**：將資料庫中的歷史數據，以受控的時間流速餵給現有的 `Matching Engine`。
* **滑點與成本模擬**：在回測階段引入滑點 (Slippage) 與分層手續費，確保回測績效不失真。
* **績效分析報表**：自動計算夏普比率 (Sharpe Ratio)、最大回撤 (MDD) 與盈虧比。

### 2.2 策略執行微服務 (Strategy Executor)
* **容器化運作**：每個交易策略作為獨立的 Thread 或 Sidecar 容器運行。
* **訊號驅動**：監聽 NATS/Redis 事件，產出 Buy/Sell 訊號後調用 `Order Service`。
* **風控守門員 (Risk Manager)**：在正式下單前進行單筆最大金額、槓桿限制的強制校驗。

---

## 3. 未來願景 (Phase 9+: Intelligent Trading & Portfolio)
* **資產管理儀表板**：監控多交易所即時資產分配。
* **機器學習訊號優化**：利用歷史數據訓練模型，優化網格交易或 MA 參數。
* **社群跟單系統**：將優質的回測策略轉化為可訂閱的信號源。

---

## 實作優先順序建議
1. **完成 ECS 壓測**（現階段關鍵，確保核心穩固）。
2. **建立 Data-Sync 服務**（開始累積自己的歷史大數據）。
3. **實作 Backtest 驅動層**（讓現有的撮合引擎動起來）。
4. **開發前端 Dashboard**（可視化回測結果）。
