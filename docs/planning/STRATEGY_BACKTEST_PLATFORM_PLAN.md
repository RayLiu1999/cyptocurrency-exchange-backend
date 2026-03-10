# 多交易所策略回測與模擬交易平台計劃書 (CCXT 架構)

## 1. 概述

本計劃旨在將現有高效能交易所系統擴展為**多交易所策略回測與模擬交易平台**。透過整合 CCXT (CryptoCurrency eXchange Trading Library) 抽象層，支援全球 100+ 交易所（如 Binance, OKX, Bybit 等）的即時行情與歷史數據，並提供統一的策略驗證與績效分析環境。

### 核心目標
- 🔗 **多交易所集成**：透過 CCXT 建立統一的 REST API 與 WebSocket 數據接入
- 📊 **高精度回測**：支援歷史 K 線回放，並結合自研撮合引擎進行成交模擬
- 🤖 **抽象策略框架**：編寫一次策略，即可在不同交易所進行回測或模擬交易
- 📈 **量化績效分析**：計算夏普比率、最大回撤等專業金融指標
- 🏛️ **雙模態並行設計**：保留 Stage 1 自研撮合引擎作為「系統模擬 (INTERNAL)」，同時接入 CCXT 作為「市場模擬 (PAPER)」，兩條軌道並行共存，前端透過 `TradingEnvironmentContext` 隨時切換
- 🎛️ **與微服務整合**：作為獨立服務運行，與 Order 及 Matching 服務解耦

---

## 2. 架構設計

### 2.1 整體分層

```
┌─────────────────────────────────────┐
│      Frontend (React/Vue)           │
│  - 策略編輯器 / 回測報告儀表板      │
│  - 即時多交易所資產監控             │
└────────────┬────────────────────────┘
             │
┌────────────▼────────────────────────┐
│      API Gateway (Gin)              │
│  - /strategy/* /backtest/*          │
│  - /exchange/* (多交易所數據查詢)   │
└────────────┬────────────────────────┘
             │
┌────────────▼────────────────────────┐
│      Service Layer                  │
│  ┌──────────────────────────────┐   │
│  │ Strategy Executor            │   │
│  │ - 策略邏輯執行、訊號生成     │   │
│  └────────────┬─────────────────┘   │
│               │                     │
│  ┌────────────▼─────────────────┐   │
│  │ Backtest Engine              │   │
│  │ - 歷史數據驅動、撮合模擬     │   │
│  └──────────────────────────────┘   │
│  ┌──────────────────────────────┐   │
│  │ Exchange Adapter (CCXT Hub)  │   │
│  │ - 統一各交易所 API、限流管理 │   │
│  └──────────────────────────────┘   │
└────────────┬────────────────────────┘
             │
┌────────────▼────────────────────────┐
│      Data Layer                     │
│  - TimescaleDB (K線、交易數據)     │
│  - Redis (即時價格、API Key 加密)   │
└─────────────────────────────────────┘
```

### 2.2 核心模組

| 模組 | 位置 | 職責 |
|------|------|------|
| Exchange Adapter | `internal/exchange/adapter.go` | 定義標準介面，封裝 CCXT 調用 |
| Market Sync Service | `internal/exchange/sync.go` | 多交易所 K 線定時同步與缺口修補 |
| Strategy Engine | `internal/strategy/engine.go` | 策略生命週期管理 |
| Backtest Engine | `internal/backtest/engine.go` | 歷史數據回放與指標計算 |

---

## 3. 實現階段

### 階段 1：多交易所數據適配（第 1-2 週）

#### 1.1 Exchange Adapter (CCXT Inspired)
**檔案：** `internal/exchange/interface.go`

```go
// 統一交易所介面
type ExchangeProvider interface {
    GetId() string
    FetchKlines(symbol string, interval string, since int64) ([]*KLine, error)
    FetchOrderBook(symbol string) (*OrderBook, error)
    FetchBalance() (*Balance, error)
    // 即時數據由各交易所 Native WS 處理或 CCXT Pro
}
```

#### 1.2 數據同步服務
*   **多路同步**：同時支持 Binance_USDT, OKX_USDT 等多個交易所的同步任務。
*   **標準化儲存**：不論來源為何，統一存入 `market_data_klines` 表。

#### 1.3 資料庫結構更新
```sql
-- 標準化 K 線表
CREATE TABLE market_data_klines (
    time TIMESTAMP NOT NULL,
    exchange_id VARCHAR(50),
    symbol VARCHAR(50),
    interval VARCHAR(10),
    open DECIMAL, high DECIMAL, low DECIMAL, close DECIMAL,
    volume DECIMAL
);
-- 轉換為時序表 (TimescaleDB)
SELECT create_hypertable('market_data_klines', 'time');
```

---

### 階段 2：通用策略引擎（第 3-4 週）

#### 2.1 策略邏輯抽象
策略不再與「幣安」綁定，而是透過傳入 `ExchangeID` 來切換：
```go
func (s *MAChrossStrategy) OnKLine(kline *KLine) {
    // 邏輯保持一致，透過 context 獲得當前交易所資訊
    if s.isGoldenCross() {
        s.executor.PlaceOrder(OrderRequest{
            Exchange: kline.ExchangeID,
            Symbol: kline.Symbol,
            Side: "BUY",
        })
    }
}
```

#### 2.2 內建策略實現

##### 2.2.1 移動平均線交叉策略
**檔案：** `internal/strategy/ma_cross.go`

```go
type MAChrossStrategy struct {
    fastPeriod   int
    slowPeriod   int
    position     decimal.Decimal  // 當前持倉
    fastMA       []decimal.Decimal
    slowMA       []decimal.Decimal
}

// 邏輯：
// - 快線向上穿越慢線 → BUY 信號
// - 快線向下穿越慢線 → SELL 信號
// - 止損：價格下跌 X%
// - 止盈：價格上漲 Y%
```

**配置示例：**
```json
{
    "name": "MA Cross",
    "parameters": {
        "fast_period": 12,
        "slow_period": 26,
        "stop_loss_pct": 2.0,
        "take_profit_pct": 5.0
    }
}
```

##### 2.2.2 RSI 超買超賣策略
**檔案：** `internal/strategy/rsi_strategy.go`

```go
type RSIStrategy struct {
    period      int
    overbought  int  // 預設 70
    oversold    int  // 預設 30
    rsiValues   []float64
}

// 邏輯：
// - RSI > overbought → 空頭信號（考慮減倉/做空）
// - RSI < oversold → 多頭信號（考慮加倉/做多）
// - 配合量價確認信號強度
```

##### 2.2.3 網格交易策略
**檔案：** `internal/strategy/grid_strategy.go`

```go
type GridStrategy struct {
    gridLevels    int
    gridSpacing   decimal.Decimal
    capital       decimal.Decimal
    gridOrders    []*GridOrder
}

// 邏輯：
// - 在價格區間內均勻設置網格
// - 自動在網格線上掛單
// - 成交後自動掛對方向訂單
// - 適合震盪行情
```

#### 2.3 策略執行框架
**檔案：** `internal/strategy/executor.go`

```go
type Executor struct {
    strategy   Strategy
    portfolio  *Portfolio
    eventChan  chan Event
    stopChan   chan struct{}
}

func (e *Executor) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        case event := <-e.eventChan:
            // 分發事件給策略
            switch ev := event.(type) {
            case *KLineEvent:
                if err := e.strategy.OnKLine(ev.KLine); err != nil {
                    log.Printf("Strategy error: %v", err)
                }
            case *OrderBook:
                if err := e.strategy.OnOrderBook(ev); err != nil {
                    log.Printf("Strategy error: %v", err)
                }
            }
        }
    }
}
```

---

### 階段 3：回測引擎（第 5-6 週）

#### 3.1 回測框架設計
**檔案：** `internal/backtest/engine.go`

```go
type BacktestEngine struct {
    strategy       Strategy
    exchange       string    // 新增：目標交易所
    startTime      time.Time
    endTime        time.Time
    initialCapital decimal.Decimal
    klines         []*KLine  // 歷史 K 線
    trades         []*Trade  // 歷史成交
    
    portfolio      *Portfolio
    orders         []*Order
    results        *BacktestResult
}

type BacktestResult struct {
    TotalReturn          decimal.Decimal
    WinRate              float64
    MaxDrawdown          decimal.Decimal
    SharpeRatio          float64
    CalmarRatio          float64
    TotalTrades          int
    WinningTrades        int
    LosingTrades         int
    AvgWin               decimal.Decimal
    AvgLoss              decimal.Decimal
    ProfitFactor         float64
    Trades               []*ExecutedTrade
}

type ExecutedTrade struct {
    EntryTime  time.Time
    EntryPrice decimal.Decimal
    ExitTime   time.Time
    ExitPrice  decimal.Decimal
    Quantity   decimal.Decimal
    PnL        decimal.Decimal
    PnLPct     float64
}
```

#### 3.2 回放與撮合模擬
**檔案：** `internal/backtest/matcher.go`

```go
type Matcher struct {
    orderBook    *OrderBook
    trades       []*Trade
}

// 撮合邏輯：
// 1. 限價單：檢查訂單簿，匹配對方向掛單
// 2. 市價單：立即用最優價成交，數量不足則部分成交
// 3. 滑點模擬 (Slippage)：加入固定比例或基於 ATR 的動態滑點
// 4. 手續費扣除：支援各交易所標準費率 (由 CCXT 提供參考值)
// 5. 流動性限制：大額訂單對價格的衝擊 (Market Impact) 模擬
```

#### 3.3 績效計算
**檔案：** `internal/backtest/metrics.go`

```go
// 核心指標
- 總收益率 = (末端資金 - 初始資金) / 初始資金 * 100%
- 勝率 = 獲利交易 / 總交易
- 最大回撤 = (最高淨值 - 低谷淨值) / 最高淨值
- 夏普比率 = (平均日收益率 - 無風險利率) / 日收益率標準差
- 利潤因子 = 總獲利 / 總虧損
- 風險收益比 = 平均獲利 / 平均虧損
```

---

### 階段 4：API 層與前端集成（第 7-8 週）

#### 4.1 API 端點設計

##### 策略管理
```
POST   /api/v1/strategies          - 建立新策略
GET    /api/v1/strategies          - 列表
GET    /api/v1/strategies/:id      - 詳情
PUT    /api/v1/strategies/:id      - 更新
DELETE /api/v1/strategies/:id      - 刪除
POST   /api/v1/strategies/:id/run  - 執行策略
POST   /api/v1/strategies/:id/stop - 停止策略
```

##### 回測執行
```
POST   /api/v1/backtests                  - 啟動回測
GET    /api/v1/backtests                  - 列表
GET    /api/v1/backtests/:id              - 結果
GET    /api/v1/backtests/:id/trades       - 交易清單
GET    /api/v1/backtests/:id/report       - 詳細報告
```

##### 即時監控
```
GET    /api/v1/portfolio                  - 持倉狀態
GET    /api/v1/portfolio/history          - 淨值曲線
WS     /ws/portfolio                      - 實時推送
```

##### 多交易所數據
```
GET    /api/v1/exchange/:id/klines/:symbol     - K 線查詢
GET    /api/v1/exchange/:id/orderbook/:symbol  - 訂單簿
GET    /api/v1/exchange/:id/trades/:symbol     - 成交記錄
GET    /api/v1/exchange/sync/status             - 同步狀態
```

#### 4.2 前端組件規劃

| 組件 | 功能 | 頁面類型 | INTERNAL 模式 | PAPER 模式 |
|------|------|------|-------------|----------|
| Strategy Editor | 可視化/JSON 編輯策略參數 | 策略配置 | 部署至系統模擬環境 | 部署至市場模擬環境 |
| Backtest Center | 歷史數據回放、盈虧曲線、回撤分析 | 策略中心 (靜態/批次) | 本地歷史成交數據 | CCXT 歷史 K 線 |
| Trading Dashboard | 即時行情、OrderBook、模擬掛單 | 交易看板 (動態/實時) | 自研撮合引擎 + Simulator | CCXT 即時行情 + Paper Trading |
| Portfolio Monitor | 資產比例、風控指標 | 風控看板 | 本地帳戶餘額 | CCXT 帳戶資產 |

> 💡 **雙模態設計原則**：所有頁面共用同一套 UI 佈局，透過前端 `TradingEnvironmentContext` 的 `mode` 狀態決定向後端請求哪個數據源。INTERNAL = 靛紫色調，PAPER = 青綠色調，全站即時切換。詳見 [ARCHITECTURE.md §4](../architecture/ARCHITECTURE.md)。

---

## 4. 技術選型

### 後端
| 組件 | 技術 | 理由 |
|------|------|------|
| 框架 | Gin | 高效能 HTTP 框架 |
| 數據庫 | TimescaleDB | 時序數據、複雜查詢 |
| 快取 | Redis | 實時行情快取 |
| 多交易所介面 | CCXT (Go Wrapper) | 快速適配多家交易所 |
| 佇列 | NATS JetStream | 低延遲、異步任務 |
| 監控 | Prometheus | 性能指標收集 |

### 前端
| 組件 | 技術 | 理由 |
|------|------|------|
| 框架 | React 18 | 組件化、生態完善 |
| 圖表 | TradingView Lightweight Charts | 專業交易圖表 |
| 狀態管理 | Redux Toolkit | 複雜狀態管理 |
| 表單 | React Hook Form | 輕量表單驗證 |
| UI 框架 | Material-UI | 專業界面組件 |

---

## 5. 實現細節

### 5.1 交易所 API 安全管理
```go
// 加密儲存 API Keys
func (m *ExchangeManager) SaveKeys(exchangeID, apiKey, secret string) error {
    encryptedSecret := m.crypto.Encrypt(secret)
    return m.db.Save(exchangeID, apiKey, encryptedSecret)
}
```

### 5.2 回測時間加速
```go
// 模擬時間快進
func (e *BacktestEngine) ProcessKLines() {
    for _, kline := range e.klines {
        e.currentTime = kline.Time
        e.strategy.OnKLine(kline)
        e.updatePortfolio()
    }
}
```

### 5.3 風控機制
```go
type RiskManager struct {
    maxPosition      decimal.Decimal  // 單筆最大持倉
    maxLeverage      decimal.Decimal  // 最大槓桿
    dailyDrawdownLimit decimal.Decimal // 日回撤限制
    stopLossLevel    decimal.Decimal  // 止損價格
}
```

---

## 6. 性能優化

### 6.1 數據庫優化
- 使用 TimescaleDB Hypertable
- 資料保留策略 (Retention Policy)
- 持久化與緩存分離

### 6.2 快取策略
- Redis 快取最近 500 根 K 線 (各交易所常用週期)
- 高頻數據緩衝 (Buffer) 批次寫入 DB

### 6.4 安全實踐
- **Secrets 管理**：禁止在代碼中硬編碼 API Key，使用 `.env` 或 AWS Secrets Manager。
- **權限最小化**：API Key 僅開啟「讀取」與「現貨交易」。
- **IP 白名單**：在交易所後台綁定伺服器靜態 IP。

---

## 7. 測試計劃

### 7.1 單元測試
- 策略信號生成邏輯
- 績效指標計算
- CCXT 適配層隔離測試

### 7.2 集成測試
- 交易所回傳模擬 (Mocking)
- 回測引擎端到端
- 事件總線 (Event Bus) 傳遞

---

## 8. 部署與監控

### 8.1 Docker 容器化
```dockerfile
# Golang 服務
FROM golang:1.21 AS builder
WORKDIR /app
COPY . .
RUN go build -o trading-platform ./cmd/server

FROM alpine:latest
COPY --from=builder /app/trading-platform /usr/local/bin/
CMD ["trading-platform"]
```

---

## 9. 項目時程

| 階段 | 時間 | 交付物 |
|------|------|--------|
| 1 | 第 1-2 週 | CCXT 適配層、數據同步服務 |
| 2 | 第 3-4 週 | 策略引擎、3 個內建策略 |
| 3 | 第 5-6 週 | 回測引擎、績效計算 |
| 4 | 第 7-8 週 | API 層、前端原型 |

---

## 10. 風險與緩解方案

| 風險 | 影響 | 緩解方案 |
|------|------|---------|
| 交易所 API 變更 | 數據同步中斷 | 定期監控 CCXT 更新、API 版本緩衝 |
| 多交易所頻率不一 | 回測難對齊 | 使用內部標準時間戳進行數據插值處理 |
| 性能瓶頸 | 回測耗時 | 多線程並行、增量快取 |

---

## 11. 進一步擴展

### 11.1 第二階段（未來）
- ✅ 支援期貨與槓桿交易
- ✅ 機器學習信號優化
- ✅ 實盤自動跟單系統

---

## 12. 成功指標

- ✅ 支持 3+ 家主流交易所
- ✅ 單次回測耗時 < 10 秒 (萬級數據)
- ✅ 策略回測精準度差異 < 3%
- ✅ 系統 24/7 自動同步數據

---

**文檔版本：** v2.0 (CCXT 基架)  
**最後更新：** 2026-03-05  
**維護者：** 開發團隊

---

## 7. 測試計劃

### 7.1 單元測試
- 策略信號生成邏輯
- 績效指標計算
- 風控檢查

### 7.2 集成測試
- 幣安 API 模擬
- 回測引擎端到端
- WebSocket 推送

### 7.3 壓力測試
- 1000+ 根 K 線回放性能
- 即時推送 10000+ 訂閱

---

## 8. 部署與監控

### 8.1 Docker 容器化
```dockerfile
# Golang 服務
FROM golang:1.21 AS builder
WORKDIR /app
COPY . .
RUN go build -o exchange ./cmd/server

FROM alpine:latest
COPY --from=builder /app/exchange /usr/local/bin/
CMD ["exchange"]
```

### 8.2 Kubernetes 編排
- StatefulSet: PostgreSQL + Redis
- Deployment: API 服務 (副本數 3)
- CronJob: 定時同步任務

### 8.3 監控與告警
- Prometheus: 導出指標
- Grafana: 儀表板可視化
- AlertManager: 回測失敗、數據延遲告警

---

## 9. 項目時程

| 階段 | 時間 | 交付物 |
|------|------|--------|
| 1 | 第 1-2 週 | 幣安客戶端、數據同步服務 |
| 2 | 第 3-4 週 | 策略引擎、3 個內建策略 |
| 3 | 第 5-6 週 | 回測引擎、績效計算 |
| 4 | 第 7-8 週 | API 層、前端原型 |
| 測試/優化 | 第 9-10 週 | 單元/集成測試、性能調優 |
| 上線準備 | 第 11-12 週 | 文檔、部署流程、培訓 |

**總耗時：** 12 週

---

## 10. 風險與緩解方案

| 風險 | 影響 | 緩解方案 |
|------|------|---------|
| 幣安 API 變更 | 數據同步中斷 | 定期監控 API 更新、版本抽象化 |
| 極端行情下撮合不準 | 回測結果偏差 | 引入滑點、手續費、流動性限制 |
| 策略過度擬合 | 實盤虧損 | Walk-forward 驗證、Out-of-sample 測試 |
| 性能瓶頸 | 回測耗時 | 多線程並行、增量快取 |

---

## 11. 進一步擴展

### 11.1 第二階段（未來）
- ✅ 支援更多交易所（OKX、Bybit、Kraken）
- ✅ 實盤交易對接（紙交易 → 真實下單）
- ✅ 機器學習信號生成
- ✅ 投組優化（Modern Portfolio Theory）

### 11.2 高級功能
- ✅ 期貨/期權策略回測
- ✅ 策略組合管理
- ✅ 實時風險監控儀表板
- ✅ 策略市場（分享、銷售策略）

---

## 12. 成功指標

- ✅ 能夠成功回測 5+ 年歷史數據
- ✅ 單次回測耗時 < 5 秒
- ✅ 策略性能差異 < 2% (回測 vs 實盤)
- ✅ 支援 10+ 幣安交易對
- ✅ WebSocket 實時推送延遲 < 100ms
- ✅ 系統可用性 > 99.5%

---

## 附錄 A：API 請求示例

### 啟動回測
```bash
curl -X POST http://localhost:8080/api/v1/backtests \
  -H "Content-Type: application/json" \
  -d '{
    "strategy_id": "ma-cross-001",
    "symbol": "BTCUSDT",
    "start_date": "2023-01-01",
    "end_date": "2024-01-01",
    "initial_capital": 10000,
    "parameters": {
      "fast_period": 12,
      "slow_period": 26
    }
  }'
```

### 查詢回測結果
```bash
curl http://localhost:8080/api/v1/backtests/backtest-123/report
```

---

## 附錄 B：資料庫初始化腳本

見 `sql/binance_schema.sql`

---

## 附錄 C：環境變數配置

```bash
# Binance API
BINANCE_API_KEY=your_key
BINANCE_API_SECRET=your_secret

# 數據庫
DATABASE_URL=postgres://user:pass@localhost:5432/crypto_exchange

# Redis
REDIS_URL=redis://localhost:6379

# 同步配置
SYNC_INTERVAL_MINUTES=5
KLINES_RETENTION_DAYS=365
```

---

**文檔版本：** v1.0  
**最後更新：** 2026-01-23  
**維護者：** 開發團隊
