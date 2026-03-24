# 多交易所策略回測與模擬交易平台計劃書

> **階段定位**：此功能屬於 [ROADMAP.md](../planning/ROADMAP.md) 的 Phase 3（CCXT 核心功能擴充）之後的獨立子系統。

## 1. 概述

將現有交易所系統擴展為多交易所策略回測與模擬交易平台。透過 CCXT 抽象層支援多家交易所（Binance, OKX, Bybit 等），提供統一的回測、模擬交易與績效分析環境。

### 核心目標
- **多交易所集成**：透過 CCXT 建立統一 REST API 與 WebSocket 數據接入
- **高精度回測**：支援歷史 K 線回放，結合自研撮合引擎進行成交模擬
- **抽象策略框架**：編寫一次策略，跨交易所回測或模擬交易
- **雙模態設計**：INTERNAL（自研撮合引擎）與 PAPER（CCXT 即時行情）兩條軌道並行

---

## 2. 架構分層

```
Frontend (React/Vue)
  ↓
API Gateway (Gin) — /strategy/* /backtest/* /exchange/*
  ↓
Service Layer
  ├── Strategy Executor（策略邏輯執行、訊號生成）
  ├── Backtest Engine（歷史數據驅動、撮合模擬）
  └── Exchange Adapter / CCXT Hub（統一各交易所 API、限流管理）
  ↓
Data Layer
  ├── TimescaleDB（K 線、交易時序數據）
  └── Redis（即時價格快取、API Key 加密儲存）
```

### 核心模組

| 模組 | 位置 | 職責 |
|:---|:---|:---|
| Exchange Adapter | `internal/exchange/adapter.go` | 標準介面，封裝 CCXT 調用 |
| Market Sync | `internal/exchange/sync.go` | 多交易所 K 線定時同步與缺口修補 |
| Strategy Engine | `internal/strategy/engine.go` | 策略生命週期管理 |
| Backtest Engine | `internal/backtest/engine.go` | 歷史數據回放與指標計算 |

---

## 3. 實現階段

| 階段 | 時間 | 交付物 |
|:---|:---|:---|
| 1 | 第 1-2 週 | CCXT 適配層、多交易所數據同步服務 |
| 2 | 第 3-4 週 | 策略引擎、3 個內建策略（MA Cross, RSI, Grid） |
| 3 | 第 5-6 週 | 回測引擎、績效計算（夏普比率、最大回撤等） |
| 4 | 第 7-8 週 | API 層、前端原型 |

---

## 4. Exchange Adapter 介面

```go
type ExchangeProvider interface {
    GetId() string
    FetchKlines(symbol string, interval string, since int64) ([]*KLine, error)
    FetchOrderBook(symbol string) (*OrderBook, error)
    FetchBalance() (*Balance, error)
}
```

### 數據表結構
```sql
CREATE TABLE market_data_klines (
    time TIMESTAMP NOT NULL,
    exchange_id VARCHAR(50),
    symbol VARCHAR(50),
    interval VARCHAR(10),
    open DECIMAL, high DECIMAL, low DECIMAL, close DECIMAL,
    volume DECIMAL
);
SELECT create_hypertable('market_data_klines', 'time');
```

---

## 5. 回測引擎核心指標

- 總收益率、勝率、最大回撤
- 夏普比率 (Sharpe Ratio)、卡瑪比率 (Calmar Ratio)
- 利潤因子 (Profit Factor)
- 滑點模擬 + 手續費扣除 + 流動性限制

---

## 6. API 端點

### 策略管理
```
POST/GET/PUT/DELETE /api/v1/strategies[/:id]
POST /api/v1/strategies/:id/run|stop
```

### 回測執行
```
POST/GET /api/v1/backtests[/:id]
GET /api/v1/backtests/:id/trades|report
```

### 多交易所數據
```
GET /api/v1/exchange/:id/klines|orderbook|trades/:symbol
GET /api/v1/exchange/sync/status
```

---

## 7. 前端雙模態對應

| 組件 | INTERNAL 模式 | PAPER 模式 |
|:---|:---|:---|
| Backtest Center | 本地歷史成交數據 | CCXT 歷史 K 線 |
| Trading Dashboard | 自研撮合引擎 + Simulator | CCXT 即時行情 + Paper Trading |
| Portfolio Monitor | 本地帳戶餘額 | CCXT 帳戶資產 |

> INTERNAL = 靛紫色調，PAPER = 青綠色調，全站即時切換。

---

## 8. 安全與風控

- **Secrets 管理**：禁止硬編碼 API Key，使用 `.env` 或 Secrets Manager
- **權限最小化**：API Key 僅開啟「讀取 + 現貨交易」
- **IP 白名單**：在交易所後台綁定伺服器靜態 IP
- **風控機制**：單筆最大持倉、最大槓桿、日回撤限制、止損價格

---

## 9. 風險與緩解

| 風險 | 緩解方案 |
|:---|:---|
| 交易所 API 變更 | 定期監控 CCXT 更新、API 版本緩衝 |
| 多交易所頻率不一 | 內部標準時間戳進行數據插值 |
| 策略過度擬合 | Walk-forward 驗證、Out-of-sample 測試 |
