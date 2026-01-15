# 高流量模擬測試指南 (Load Testing Guide)

本文件詳細描述如何對加密貨幣交易所進行高流量模擬測試，包括測試環境搭建、工具選擇、測試場景設計和結果分析。

---

## 1. 測試目標與指標

### 1.1 核心測試目標
- **吞吐量 (Throughput)**: 系統每秒能處理的交易數量 (TPS)
- **延遲 (Latency)**: 從下單請求到收到回應的時間
- **可用性 (Availability)**: 在高負載下的成功率
- **資源使用率 (Resource Utilization)**: CPU、記憶體、網路頻寬的使用情況
- **穩定性 (Stability)**: 長時間運行下是否有洩漏或崩潰

### 1.2 性能基準 (SLA)

| 指標 | 目標值 | 說明 |
|------|-------|------|
| 平均延遲 (P50) | < 100ms | 50% 的請求在 100ms 內完成 |
| 95 分位延遲 (P95) | < 500ms | 95% 的請求在 500ms 內完成 |
| 99 分位延遲 (P99) | < 2s | 99% 的請求在 2s 內完成 |
| 吞吐量 | > 1000 TPS | 至少支援 1000 筆交易/秒 |
| 成功率 | > 99.5% | 故障率低於 0.5% |
| CPU 使用率 | < 80% | 留有冗餘容量 |
| 記憶體使用率 | < 75% | 防止 OOM (Out of Memory) |

---

## 2. 測試環境搭建

### 2.1 本地測試環境

#### 推薦配置

```yaml
# docker-compose.test.yml
version: '3.8'

services:
  # PostgreSQL
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: test_user
      POSTGRES_PASSWORD: test_pass
      POSTGRES_DB: exchange_test
    ports:
      - "5432:5432"
    volumes:
      - test_postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U test_user"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Redis
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    command: redis-server --maxmemory 512mb --maxmemory-policy allkeys-lru
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Kafka + Zookeeper
  zookeeper:
    image: confluentinc/cp-zookeeper:7.5.0
    environment:
      ZOOKEEPER_CLIENT_PORT: 2181
    ports:
      - "2181:2181"

  kafka:
    image: confluentinc/cp-kafka:7.5.0
    depends_on:
      - zookeeper
    environment:
      KAFKA_BROKER_ID: 1
      KAFKA_ZOOKEEPER_CONNECT: zookeeper:2181
      KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
    ports:
      - "9092:9092"

  # 監控：Prometheus
  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus_data:/prometheus
    ports:
      - "9090:9090"
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'

  # 監控：Grafana
  grafana:
    image: grafana/grafana:latest
    environment:
      GF_SECURITY_ADMIN_PASSWORD: admin
    ports:
      - "3000:3000"
    volumes:
      - grafana_data:/var/lib/grafana

volumes:
  test_postgres_data:
  prometheus_data:
  grafana_data:
```

#### 啟動命令

```bash
# 啟動測試環境
docker-compose -f docker-compose.test.yml up -d

# 驗證所有服務已啟動
docker-compose -f docker-compose.test.yml ps

# 初始化資料庫
docker-compose -f docker-compose.test.yml exec postgres \
  psql -U test_user -d exchange_test -f /sql/schema.sql
```

### 2.2 本地應用服務啟動

```bash
# 設定環境變數
export DATABASE_URL="postgres://test_user:test_pass@localhost:5432/exchange_test?sslmode=disable"
export REDIS_URL="redis://localhost:6379"
export KAFKA_BROKERS="localhost:9092"

# 編譯並運行服務（需要優化以支持高併發）
go build -o bin/exchange-server ./cmd/server/main.go
./bin/exchange-server
```

---

## 3. 測試工具選擇

### 3.1 推薦工具

#### Apache JMeter (開源、功能完整)
適合複雜的業務邏輯測試，支援Groovy腳本自訂化。

**安裝**:
```bash
# macOS
brew install jmeter

# Linux
wget https://archive.apache.org/dist/jmeter/binaries/apache-jmeter-5.6.tgz
tar -xzf apache-jmeter-5.6.tgz
```

#### k6 (現代、以代碼為中心)
用 JavaScript 編寫測試，輕量級且易於集成 CI/CD。

**安裝**:
```bash
# macOS
brew install k6

# Linux
sudo apt-key adv --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
echo "deb https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6-stable.list
sudo apt-get update
sudo apt-get install k6
```

#### Vegeta (極簡化、適合單點測試)
專注於 HTTP 負載測試，命令行友善。

**安裝**:
```bash
# macOS
brew install vegeta

# Linux
go install github.com/tsenart/vegeta@latest
```

### 3.2 本文重點：使用 k6 進行測試

k6 的優勢：
- **輕量級**: 單個 Go 二進位檔，無依賴
- **易於集成**: JavaScript 腳本，支援 VU (Virtual Users) 模型
- **完整指標**: 自動收集 HTTP 指標，支援自訂化
- **實時報告**: 可整合 Grafana 展示實時結果

---

## 4. 測試場景設計

### 4.1 測試場景

#### 場景 1：峰值負載測試 (Spike Test)
模擬突然的流量尖峰，驗證系統的快速反應能力。

```javascript
// tests/spike_test.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

// 自訂指標
const failureRate = new Rate('failures');
const placingOrderDuration = new Trend('placing_order_duration');
const cancelOrderDuration = new Trend('cancel_order_duration');

// 測試配置
export const options = {
  stages: [
    { duration: '1m', target: 100 },     // 1 分鐘內逐漸達到 100 VU
    { duration: '2m', target: 500 },     // 2 分鐘內快速上升到 500 VU（尖峰）
    { duration: '2m', target: 100 },     // 2 分鐘內快速下降
    { duration: '1m', target: 0 },       // 1 分鐘內逐漸降至 0
  ],
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<2000'], // P95 < 500ms, P99 < 2s
    http_req_failed: ['rate<0.005'],     // 失敗率 < 0.5%
    failures: ['rate<0.005'],
  },
};

// 模擬用戶行為
export default function () {
  const baseURL = 'http://localhost:8080';
  const userId = `user_${__VU}_${__ITER}`; // 每個 VU 獲得唯一 ID
  
  // 1. 下單
  const placeOrderPayload = JSON.stringify({
    user_id: userId,
    symbol: 'BTCUSD',
    side: 'BUY',
    type: 'LIMIT',
    price: 45000,
    quantity: 1.5,
  });

  const placeOrderRes = http.post(`${baseURL}/orders`, placeOrderPayload, {
    headers: { 'Content-Type': 'application/json' },
  });

  placingOrderDuration.add(placeOrderRes.timings.duration);
  failureRate.add(placeOrderRes.status !== 201);

  check(placeOrderRes, {
    'place order status is 201': (r) => r.status === 201,
    'place order response time < 500ms': (r) => r.timings.duration < 500,
  });

  if (placeOrderRes.status !== 201) {
    console.error(`Place order failed: ${placeOrderRes.status} - ${placeOrderRes.body}`);
  }

  sleep(0.5); // 隨機休眠 0-1 秒，模擬用戶思考時間
}
```

#### 場景 2：持續負載測試 (Sustained Load Test)
模擬正常工作日的穩定流量。

```javascript
// tests/sustained_load_test.js
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  stages: [
    { duration: '2m', target: 300 },     // Ramp up
    { duration: '10m', target: 300 },    // 持續 10 分鐘穩定負載
    { duration: '2m', target: 0 },       // Ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<300', 'p(99)<1000'],
    http_req_failed: ['rate<0.001'],     // 失敗率 < 0.1%
  },
};

export default function () {
  const baseURL = 'http://localhost:8080';
  const userId = `user_${__VU}`;
  
  // 模擬多種操作：下單、查詢、撤單
  const operations = ['place_order', 'get_order', 'cancel_order'];
  const op = operations[Math.floor(Math.random() * operations.length)];

  let res;
  if (op === 'place_order') {
    const payload = JSON.stringify({
      user_id: userId,
      symbol: 'ETHUSD',
      side: Math.random() > 0.5 ? 'BUY' : 'SELL',
      type: 'LIMIT',
      price: 2500 + Math.random() * 100,
      quantity: Math.random() * 10,
    });
    res = http.post(`${baseURL}/orders`, payload, {
      headers: { 'Content-Type': 'application/json' },
    });
  } else if (op === 'get_order') {
    const orderId = `order_${Math.floor(Math.random() * 10000)}`;
    res = http.get(`${baseURL}/orders/${orderId}`);
  }

  check(res, {
    'status is 2xx or 404': (r) => r.status >= 200 && r.status < 300 || r.status === 404,
  });
}
```

#### 場景 3：應力測試 (Stress Test)
逐漸增加負載至系統崩潰，找出最大承載量。

```javascript
// tests/stress_test.js
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  stages: [
    { duration: '2m', target: 100 },
    { duration: '2m', target: 500 },
    { duration: '2m', target: 1000 },
    { duration: '2m', target: 2000 },    // 施加額外壓力
    { duration: '3m', target: 2000 },    // 維持高負載
    { duration: '2m', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(99)<5000'],   // 容許更高的延遲
    http_req_failed: ['rate<0.1'],       // 容許 10% 失敗率
  },
};

export default function () {
  const baseURL = 'http://localhost:8080';
  const payload = JSON.stringify({
    user_id: `stress_user_${__VU}`,
    symbol: 'BTCUSD',
    side: 'BUY',
    type: 'MARKET',
    quantity: 0.1,
  });

  const res = http.post(`${baseURL}/orders`, payload, {
    headers: { 'Content-Type': 'application/json' },
  });

  check(res, {
    'request completed': (r) => r.status > 0,
  });
}
```

---

## 5. 測試執行與監控

### 5.1 運行 k6 測試

```bash
# 場景 1：峰值負載測試
k6 run --vus 500 --duration 6m tests/spike_test.js

# 場景 2：持續負載測試
k6 run --vus 300 --duration 14m tests/sustained_load_test.js

# 場景 3：應力測試
k6 run tests/stress_test.js

# 输出到 JSON 格式，便於分析
k6 run --vus 500 --duration 6m --out json=results.json tests/spike_test.js

# 輸出到 Grafana Cloud（需配置）
k6 run --out cloud tests/spike_test.js
```

### 5.2 實時監控儀表板

#### Prometheus 配置

```yaml
# prometheus.yml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'exchange-server'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'

  - job_name: 'postgres'
    static_configs:
      - targets: ['localhost:5432']

  - job_name: 'redis'
    static_configs:
      - targets: ['localhost:6379']
```

#### Grafana 儀表板配置

1. 在 Grafana 中新增 Prometheus 資料源：
   - URL: `http://localhost:9090`

2. 導入預設的 k6 儀表板：
   - ID: `2587`（k6 官方儀表板）

3. 創建自訂圖表監控：
   - **吞吐量 (TPS)**: `rate(http_requests_total[1m])`
   - **P95 延遲**: `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[1m]))`
   - **錯誤率**: `rate(http_requests_failed_total[1m])`
   - **CPU 使用率**: `node_cpu_usage_percent`
   - **記憶體使用率**: `node_memory_usage_percent`

### 5.3 系統監控指令

```bash
# 1. 監控 Go 進程的 CPU 和記憶體
top -p $(pgrep -f exchange-server)

# 2. 監控 PostgreSQL 連接數
psql -U test_user -d exchange_test -c "SELECT count(*) FROM pg_stat_activity;"

# 3. 監控 Redis 記憶體
redis-cli INFO memory

# 4. 監控 Kafka 吞吐量
kafka-consumer-perf-test --broker-list localhost:9092 --topic orders --messages 10000 --threads 1

# 5. 實時查看網路連接
netstat -an | grep ESTABLISHED | wc -l
```

---

## 6. 測試結果分析

### 6.1 k6 輸出示例

```
     data_received..................: 5.2 MB   26 kB/s
     data_sent........................: 2.8 MB   14 kB/s
     http_req_blocked..................: avg=50.23ms min=10.12ms  med=35.45ms max=250.32ms p(90)=120.34ms p(95)=145.67ms
     http_req_connecting...............: avg=30.12ms min=5.23ms   med=20.34ms max=180.12ms p(90)=90.23ms  p(95)=110.45ms
     http_req_duration.................: avg=250.45ms min=45.23ms  med=180.34ms max=2500.45ms p(90)=450.23ms p(95)=680.34ms p(99)=1200.45ms ✓
     http_req_failed...................: 0.23% ✓
     http_req_receiving................: avg=15.23ms  min=1.12ms   med=10.23ms  max=120.34ms p(90)=45.23ms  p(95)=65.45ms
     http_req_sending..................: avg=5.12ms   min=0.23ms   med=3.45ms   max=50.23ms  p(90)=15.23ms  p(95)=20.34ms
     http_req_tls_handshaking..........: avg=0ms      min=0ms      med=0ms      max=0ms      p(90)=0ms      p(95)=0ms
     http_req_waiting..................: avg=230.10ms min=40.12ms  med=160.45ms max=2300.34ms p(90)=420.34ms p(95)=640.23ms
     http_reqs..........................: 18450   92.25 reqs/s
     iteration_duration................: avg=251.45ms min=46.23ms  med=181.45ms max=2501.56ms p(90)=451.34ms p(95)=681.45ms
     iterations.........................: 18450   92.25 iters/s
     vus...............................: 500    
     vus_max............................: 500
```

### 6.2 結果解讀

| 指標 | 解釋 | 良好範圍 |
|------|------|---------|
| `http_req_duration (P95)` | 95% 請求的完成時間 | < 500ms |
| `http_req_failed` | 失敗率 | < 0.5% |
| `http_reqs / reqs/s` | 吞吐量 (TPS) | > 1000 |
| `iteration_duration (avg)` | 平均迭代時間 | < 500ms |

### 6.3 瓶頸診斷

**問題 1: 延遲高**
```bash
# 檢查 Database 連接池
# 在 service.go 中檢查 pgxpool 配置
// 調整連接池大小
connConfig, _ := pgxpool.ParseConfig(dbURL)
connConfig.MaxConns = 50  // 增大連接池
connConfig.MinConns = 10
```

**問題 2: 記憶體洩漏**
```bash
# 使用 pprof 分析記憶體
go tool pprof http://localhost:6060/debug/pprof/heap

# 在 main.go 中註冊 pprof
import _ "net/http/pprof"
go func() {
  log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

**問題 3: Kafka 延遲**
```bash
# 檢查 Kafka 消費者延遲
kafka-consumer-groups --bootstrap-server localhost:9092 \
  --group order-consumer-group --describe

# 調整 batch.size 和 linger.ms
producer := kafka.NewProducer(&kafka.ConfigMap{
  "bootstrap.servers": "localhost:9092",
  "batch.size":        16384,  // 增大 batch
  "linger.ms":         100,    // 增大 linger 時間以累積更多訊息
})
```

---

## 7. 性能優化建議

### 7.1 代碼級優化

#### 1. 連接池配置

```go
// internal/repository/postgres.go
func NewPostgresRepository(dbURL string) (*PostgresRepository, error) {
  config, _ := pgxpool.ParseConfig(dbURL)
  
  // 根據預期的並發用戶數調整
  config.MaxConns = 50           // 最大連接數
  config.MinConns = 10           // 最小連接數
  config.MaxConnLifetime = 5 * time.Minute
  config.MaxConnIdleTime = 2 * time.Minute
  
  pool, err := pgxpool.NewWithConfig(context.Background(), config)
  return &PostgresRepository{db: pool}, err
}
```

#### 2. Redis 緩存層

```go
// internal/api/handlers.go (新增緩存)
type CachedHandler struct {
  svc    core.ExchangeService
  cache  *redis.Client
}

func (h *CachedHandler) GetOrderBook(c *gin.Context) {
  symbol := c.Query("symbol")
  cacheKey := fmt.Sprintf("orderbook:%s", symbol)
  
  // 優先查詢緩存
  val, err := h.cache.Get(c.Request.Context(), cacheKey).Result()
  if err == nil {
    c.JSON(200, json.Unmarshal([]byte(val)))
    return
  }
  
  // 緩存未命中，從 DB 查詢
  orderBook := h.svc.GetOrderBook(c.Request.Context(), symbol)
  h.cache.Set(c.Request.Context(), cacheKey, orderBook, 5*time.Second) // 5秒過期
  c.JSON(200, orderBook)
}
```

#### 3. 批量操作

```go
// internal/repository/postgres.go (批量插入 Trades)
func (r *PostgresRepository) CreateTradesBatch(ctx context.Context, trades []*core.Trade) error {
  batch := &pgx.Batch{}
  
  for _, trade := range trades {
    batch.Queue(`
      INSERT INTO trades (id, maker_order_id, taker_order_id, symbol, price, quantity, created_at)
      VALUES ($1, $2, $3, $4, $5, $6, $7)
    `, trade.ID, trade.MakerOrderID, trade.TakerOrderID, trade.Symbol, trade.Price, trade.Quantity, trade.CreatedAt)
  }
  
  results := r.db.SendBatch(ctx, batch)
  defer results.Close()
  
  for _, trade := range trades {
    if _, err := results.Exec(); err != nil {
      return fmt.Errorf("batch insert failed: %w", err)
    }
  }
  return nil
}
```

### 7.2 基礎設施優化

| 優化項目 | 配置示例 | 預期效果 |
|---------|---------|---------|
| **PostgreSQL 索引** | 在 `orders.symbol`, `orders.status`, `orders.price` 上建立索引 | 查詢速度 10x 提升 |
| **Redis 集群** | 部署 Redis Sentinel 或 Cluster | 高可用性 + 分散式緩存 |
| **Kafka 分區** | 為每個 symbol 分配不同的 partition | 並行處理能力 |
| **Load Balancer** | Nginx/HAProxy 配置 upstream | 流量均衡，故障轉移 |

### 7.3 性能測試迭代循環

```
1. 基線測試 (Baseline)
   ↓
2. 識別瓶頸 (Bottleneck Analysis)
   ↓
3. 實施優化 (Optimization)
   ↓
4. 再次測試 (Re-test)
   ↓
5. 測量改進 (Measure Improvement)
   ↓
6. 確認達到 SLA (Validation)
```

---

## 8. CI/CD 集成

### 8.1 GitHub Actions 配置

```yaml
# .github/workflows/load-test.yml
name: Load Testing

on:
  pull_request:
  schedule:
    - cron: '0 2 * * *'  # 每天凌晨 2 點執行

jobs:
  load-test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:15-alpine
        env:
          POSTGRES_PASSWORD: test_pass
          POSTGRES_DB: exchange_test
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 5432:5432

      redis:
        image: redis:7-alpine
        options: >-
          --health-cmd "redis-cli ping"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 6379:6379

      kafka:
        image: confluentinc/cp-kafka:7.5.0
        env:
          KAFKA_ZOOKEEPER_CONNECT: zookeeper:2181
          KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://localhost:9092
        ports:
          - 9092:9092

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: 1.21

      - name: Set up k6
        run: |
          sudo apt-key adv --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
          echo "deb https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6-stable.list
          sudo apt-get update
          sudo apt-get install k6

      - name: Build and start server
        env:
          DATABASE_URL: postgres://postgres:test_pass@localhost:5432/exchange_test
          REDIS_URL: redis://localhost:6379
          KAFKA_BROKERS: localhost:9092
        run: |
          go build -o bin/exchange-server ./cmd/server/main.go
          ./bin/exchange-server &
          sleep 5

      - name: Run Load Tests
        run: k6 run --out json=results.json tests/sustained_load_test.js

      - name: Upload results
        uses: actions/upload-artifact@v3
        with:
          name: load-test-results
          path: results.json

      - name: Comment on PR
        if: github.event_name == 'pull_request'
        uses: actions/github-script@v6
        with:
          script: |
            const fs = require('fs');
            const results = JSON.parse(fs.readFileSync('results.json', 'utf8'));
            const comment = `## Load Test Results\n\`\`\`json\n${JSON.stringify(results.metrics, null, 2)}\n\`\`\``;
            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: comment
            });
```

---

## 9. 常見問題 (FAQ)

### Q: 本地測試得到 1000 TPS，但上線後只有 500 TPS？
**A**: 常見原因：
1. **網路延遲**: 本地測試無網路延遲，上線環境有跨地域延遲
2. **資料庫瓶頸**: 本地用小資料集，上線有大量歷史資料導致查詢變慢
3. **外部依賴**: 區塊鏈節點同步延遲、Kafka broker 壓力等

**解決方案**: 在測試環境中模擬真實的網路延遲和資料量

### Q: 高負載下記憶體持續增長？
**A**: 可能是 goroutine 洩漏或 channel 積壓
```go
// 添加 goroutine 監控
import "runtime"
go func() {
  ticker := time.NewTicker(10 * time.Second)
  for range ticker.C {
    fmt.Printf("Goroutines: %d\n", runtime.NumGoroutine())
  }
}()
```

### Q: 如何測試撮合引擎的性能？
**A**: 針對撮合引擎的專項測試
```javascript
// tests/matching_engine_test.js
export default function() {
  const payload = JSON.stringify({
    user_id: `match_user_${__VU}`,
    symbol: 'BTCUSD',
    side: Math.random() > 0.5 ? 'BUY' : 'SELL',
    type: 'LIMIT',
    price: 45000 + Math.random() * 1000,
    quantity: Math.random() * 5,
  });
  
  http.post('http://localhost:8080/orders', payload);
  // 測試撮合率、成交延遲等
}
```

---

## 10. 進階主題

### 10.1 分散式負載測試

使用多台機器進行測試，更接近真實環境：

```bash
# 主控機器
k6 run -o cloud tests/spike_test.js

# 或使用 k6 Cloud 服務（需付費）
k6 cloud tests/spike_test.js
```

### 10.2 長期穩定性測試 (Soak Test)

```javascript
// tests/soak_test.js
export const options = {
  stages: [
    { duration: '10m', target: 200 },
    { duration: '2h', target: 200 },    // 持續 2 小時
    { duration: '10m', target: 0 },
  ],
};
```

### 10.3 混合場景測試

同時模擬多種交易對和操作：

```javascript
const symbols = ['BTCUSD', 'ETHUSD', 'ADAUSD'];
const symbol = symbols[Math.floor(Math.random() * symbols.length)];
// 下單邏輯...
```

---

## 總結

高流量測試是確保交易所穩定性的關鍵步驟。透過系統化的測試方法和工具，可以提前發現瓶頸並進行優化，為上線做好充分準備。
