# 幣安 API 整合與策略回測平台計劃書

## 1. 概述

本計劃旨在將現有高效能交易所系統擴展為**策略回測與模擬交易平台**，集成幣安（Binance）即時行情與歷史數據，支援多種交易策略的驗證與優化，最終提供類似 TradingView Pine Script 的體驗。

### 核心目標
- 🔗 集成幣安 REST & WebSocket API
- 📊 支援歷史行情回放與回測
- 🤖 提供策略引擎與執行框架
- 📈 計算績效指標與風險分析
- 🎛️ 前端可視化配置與管理

---

## 2. 架構設計

### 2.1 整體分層

```
┌─────────────────────────────────────┐
│      Frontend (React/Vue)           │
│  - 策略編輯器/可視化配置            │
│  - 回測報告儀表板                   │
│  - 即時模擬交易監控                 │
└────────────┬────────────────────────┘
             │
┌────────────▼────────────────────────┐
│      API Layer (Gin)                │
│  - /strategy/*  (策略管理)          │
│  - /backtest/*  (回測執行)          │
│  - /simulation/* (即時模擬)         │
│  - /binance/*   (幣安數據同步)      │
└────────────┬────────────────────────┘
             │
┌────────────▼────────────────────────┐
│      Service Layer                  │
│  ┌──────────────────────────────┐   │
│  │ Internal Event Bus           │   │
│  │ - 解耦行情與策略信號         │   │
│  └────────────┬─────────────────┘   │
│               │                     │
│  ┌────────────▼─────────────────┐   │
│  │ Strategy Engine              │   │
│  │ - 策略執行與信號生成         │   │
│  └──────────────────────────────┘   │
│  ┌──────────────────────────────┐   │
│  │ Backtest Engine              │   │
│  │ - 歷史回放、撮合模擬(含滑點) │   │
│  └──────────────────────────────┘   │
│  ┌──────────────────────────────┐   │
│  │ Binance Provider/Sync        │   │
│  │ - 數據獲取、限流與缺口修補   │   │
│  └──────────────────────────────┘   │
└────────────┬────────────────────────┘
             │
┌────────────▼────────────────────────┐
│      Data Layer                     │
│  - PostgreSQL (交易、K線、用戶)    │
│  - Redis Cache (即時行情)           │
│  - 本地持久化 (回測結果)            │
└─────────────────────────────────────┘
```

### 2.2 核心模組

| 模組 | 位置 | 職責 |
|------|------|------|
| Binance Client | `internal/binance/client.go` | REST API 調用、認證 |
| Binance WebSocket | `internal/binance/websocket.go` | 行情推送、訂單狀態 |
| Strategy Engine | `internal/strategy/engine.go` | 策略執行、信號生成 |
| Backtest Engine | `internal/backtest/engine.go` | 歷史回放、績效計算 |
| Data Sync | `internal/binance/sync.go` | 定時數據同步 |
| Portfolio Manager | `internal/portfolio/manager.go` | 持倉、風控管理 |

---

## 3. 實現階段

### 階段 1：幣安數據集成（第 1-2 週）

#### 1.1 Binance REST API 客戶端
**檔案：** `internal/binance/client.go`

```go
type BinanceClient struct {
    apiKey    string
    apiSecret string
    baseURL   string
    client    *http.Client
}

// 方法清單
- GetKlines(symbol string, interval string, limit int) ([]*KLine, error)
- GetOrderBook(symbol string, limit int) (*OrderBook, error)
- GetRecentTrades(symbol string, limit int) ([]*Trade, error)
- GetExchangeInfo() (*ExchangeInfo, error)

// 關鍵設計：權重限流器
- 基於令牌桶 (Token Bucket) 演算法
- 支援 X-MBX-USED-WEIGHT-1M 動態調整
- 分布式擴展：支援 Redis 計數器
```

**支援交易對：** BTC, ETH, SOL, ADA, DOGE 等（配置化）

**API 端點：**
- `GET /api/v3/klines` - K 線數據
- `GET /api/v3/depth` - 訂單簿
- `GET /api/v3/trades` - 近期成交
- `GET /api/v3/exchangeInfo` - 交易所信息

#### 1.2 WebSocket 實時行情推送
**檔案：** `internal/binance/websocket.go`

```go
type WebSocketHandler struct {
    conn      *websocket.Conn
    listeners map[string][]func(*StreamMessage)
}

// 訂閱流
- kline@1m  (1 分鐘 K 線)
- kline@1h  (1 小時 K 線)
- trade     (實時成交)
- depth20   (訂單簿快照)
- aggregateTrade (合併成交)
```

**特性：**
- 自動重連機制
- 消息隊列緩衝
- 事件驅動架構

#### 1.3 數據同步服務
**檔案：** `internal/binance/sync.go`

```go
type SyncService struct {
    client    *BinanceClient
    db        *pgxpool.Pool
    ticker    *time.Ticker
}

// 同步任務
- SyncKlines(symbol, interval) - 定時同步 K 線
- SyncTrades(symbol)          - 同步交易歷史
- SyncOrderBook(symbol)       - 快照持久化

// 進階機制
- 缺口檢測 (Gap Detection)：自動掃描並補齊斷線期間的 K 線數據
- 延遲過濾：剔除延遲超過 1000ms 的過時數據
```

**儲存策略：**
- 每個交易對每個時間週期單獨表
- 增量同步（記錄上次同步時間）
- 自動清理過期數據（保留 1 年）

#### 1.4 資料庫擴展
**新增表結構：**

```sql
-- 幣安 K 線數據
CREATE TABLE binance_klines (
    id SERIAL PRIMARY KEY,
    symbol VARCHAR(20),
    interval VARCHAR(10),  -- 1m, 5m, 1h, 4h, 1d
    open_time TIMESTAMP,
    open DECIMAL(20, 8),
    high DECIMAL(20, 8),
    low DECIMAL(20, 8),
    close DECIMAL(20, 8),
    volume DECIMAL(20, 2),
    quote_volume DECIMAL(20, 8),
    trades INT,
    taker_buy_volume DECIMAL(20, 2),
    synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(symbol, interval, open_time)
);

-- 幣安實時成交
CREATE TABLE binance_trades (
    id BIGINT PRIMARY KEY,
    symbol VARCHAR(20),
    price DECIMAL(20, 8),
    quantity DECIMAL(20, 8),
    trade_time TIMESTAMP,
    is_buyer_maker BOOLEAN
);

-- 訂單簿快照
CREATE TABLE orderbook_snapshots (
    id UUID PRIMARY KEY,
    symbol VARCHAR(20),
    timestamp TIMESTAMP,
    bids JSONB,
    asks JSONB
);

-- 索引
CREATE INDEX idx_binance_klines_symbol_interval ON binance_klines(symbol, interval, open_time DESC);
CREATE INDEX idx_binance_trades_symbol_time ON binance_trades(symbol, trade_time DESC);
```

---

### 階段 2：策略引擎（第 3-4 週）

#### 2.1 策略介面定義
**檔案：** `internal/strategy/interface.go`

```go
// Strategy 定義所有策略必須實現的介面
type Strategy interface {
    // 初始化
    Initialize(ctx context.Context, config StrategyConfig) error
    
    // 事件回調
    OnKLine(kline *KLine) error
    OnTrade(trade *Trade) error
    OnOrderBook(snapshot *OrderBook) error
    OnPortfolioUpdate(portfolio *Portfolio) error
    
    // 清理
    Close() error
}

type StrategyConfig struct {
    Name          string                 `json:"name"`
    Description   string                 `json:"description"`
    Parameters    map[string]interface{} `json:"parameters"`
    RiskLimits    RiskLimits             `json:"risk_limits"`
    Symbols       []string               `json:"symbols"`
}

// 交易指令
type Order struct {
    Symbol    string
    Side      string          // BUY, SELL
    Type      string          // LIMIT, MARKET
    Quantity  decimal.Decimal
    Price     decimal.Decimal
    TimeInForce string         // GTC, IOC, FOK
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
// 4. 手續費扣除：支援 0.1% 標準費率或使用虛擬 BNB 抵扣模擬
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

##### 幣安數據
```
GET    /api/v1/binance/klines/:symbol     - K 線查詢
GET    /api/v1/binance/orderbook/:symbol  - 訂單簿
GET    /api/v1/binance/trades/:symbol     - 成交記錄
GET    /api/v1/binance/sync/status        - 同步狀態
```

#### 4.2 前端組件規劃

| 組件 | 功能 | 頁面類型 |
|------|------|------|
| Strategy Editor | 可視化/JSON 編輯策略參數 | 策略配置 |
| Backtest Center | 歷史數據回放、盈虧曲線、回撤分析 | 策略中心 (靜態/批次) |
| Paper Trading | 實時行情、持倉線、模擬掛單 | 交易看板 (動態/實時) |
| Portfolio Monitor | 多幣種資產比例、風險指標 | 風控看板 |

---

## 4. 技術選型

### 後端
| 組件 | 技術 | 理由 |
|------|------|------|
| 框架 | Gin | 高效能 HTTP 框架 |
| 數據庫 | PostgreSQL | 關聯數據、複雜查詢 |
| 快取 | Redis | 實時行情快取 |
| 時序數據 | TimescaleDB | K 線高效存儲（可選） |
| 佇列 | RabbitMQ | 異步任務、數據同步 |
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

### 5.1 幣安 API 認證
```go
// HMAC-SHA256 簽名
func (c *BinanceClient) sign(params map[string]string) string {
    queryString := c.encodeParams(params)
    hash := hmac.New(sha256.New, []byte(c.apiSecret))
    hash.Write([]byte(queryString))
    return hex.EncodeToString(hash.Sum(nil))
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
- K 線表按日期分區
- 主鍵索引（symbol, interval, open_time）
- 結合 TimescaleDB 進行時序數據優化

### 6.2 快取策略
- Redis 快取最近 100 根 K 線
- 訂單簿快照 TTL 5 分鐘
- 成交記錄 LRU 快取

### 6.4 安全實踐
- **Secrets 管理**：禁止在代碼中硬編碼 API Key，使用 `.env` 或 AWS Secrets Manager。
- **權限最小化**：API Key 僅開啟「讀取」與「現貨交易」，嚴禁開啟「提現」權限。
- **IP 白名單**：在幣安後台綁定伺服器靜態 IP 以增強安全性。

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
