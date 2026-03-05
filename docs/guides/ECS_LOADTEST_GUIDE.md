# AWS ECS 部署與壓測實戰手冊

> **目標讀者**：有 VPS / K8s 經驗，想用這個交易所專案學習 AWS ECS 部署與高流量壓測的工程師。
>
> **核心策略**：用 Terraform 快速開關環境 → k6 壓測弄壞它 → 用架構演進解決問題。
>
> **選擇 ECS 而非 EKS**：ECS 不需要管 Control Plane（省 $72/月），Fargate 模式下連 Worker Node 都不用管，非常適合「快速驗證、快速關閉」的學習場景。

---

## 目錄

- [Phase 0：本地確認一切正常](#phase-0本地確認一切正常)
- [Phase 1：快速部署到 AWS（Docker + Portainer）](#phase-1快速部署到-aws)
- [Phase 2：第一次壓測，找到瓶頸](#phase-2第一次壓測找到瓶頸)
- [決策點 A：換機器 vs 調參數 vs 加快取](#決策點-a換機器-vs-調參數-vs-加快取)
- [Phase 3：引入 Redis，解決讀取熱點](#phase-3引入-redis)
- [Phase 4：遷移到 ECS，水平擴展](#phase-4遷移到-ecs水平擴展)
- [決策點 B：Race Condition 怎麼辦](#決策點-b-race-condition-怎麼辦)
- [Phase 5：引入 Kafka，異步削峰](#phase-5引入-kafka異步削峰)
- [Phase 6：可觀測性 Prometheus + Grafana](#phase-6可觀測性)
- [決策點 C：何時拆微服務](#決策點-c何時拆微服務)
- [費用控制與環境清理](#費用控制與環境清理)

---

## Phase 0：本地確認一切正常

**在上雲之前，本地必須 100% 跑通。** 雲上 debug 的代價是時間 + 金錢。

### 0-1. 啟動本地環境

```bash
# 啟動 PostgreSQL
docker run -d --name exchange-pg \
  -e POSTGRES_PASSWORD=123qwe \
  -e POSTGRES_DB=exchange \
  -p 5432:5432 postgres:15-alpine

# 等 DB 就緒，建立 schema
sleep 3
psql postgresql://postgres:123qwe@localhost:5432/exchange -f sql/schema.sql
psql postgresql://postgres:123qwe@localhost:5432/exchange -f sql/seed.sql

# 跑後端
export DATABASE_URL="postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable"
go run cmd/server/main.go
```

### 0-2. 快速驗證 API

```bash
# 健康檢查
curl -s http://localhost:8080/api/v1/orderbook?symbol=BTCUSDT | jq .

# 下一張限價買單
curl -s -X POST http://localhost:8080/api/v1/orders \
  -H "Content-Type: application/json" \
  -d '{"user_id":1,"symbol":"BTCUSDT","side":"buy","type":"limit","price":"40000","quantity":"0.01"}' | jq .

# 確認訂單簿有資料
curl -s http://localhost:8080/api/v1/orderbook?symbol=BTCUSDT | jq .
```

### 0-3. 確認檢查清單

- [ ] `GET /api/v1/orderbook` 回傳 200 且有資料
- [ ] `POST /api/v1/orders` 成功建立訂單（回傳 order ID）
- [ ] WebSocket 連線後能收到推送（用瀏覽器 DevTools 測試 `ws://localhost:8080/ws`）
- [ ] `go test ./...` 全部通過

**全部通過才繼續。**

---

## Phase 1：快速部署到 AWS

### 1-1. 前置條件

```bash
# 確認工具都裝好
aws --version        # aws-cli v2
terraform --version  # >= 1.5
docker --version

# 設定 AWS 憑證（用你的 IAM user）
aws configure
# AWS Access Key ID: xxxxxx
# AWS Secret Access Key: xxxxxx
# Default region name: ap-northeast-1   ← 東京，離台灣最近
# Default output format: json
```

**IAM 最小權限**（建議建一個專用 IAM user，不要用 root）：
- `AmazonEC2FullAccess`
- `AmazonECR_FullAccess`
- `AmazonECS_FullAccess`（Phase 4 會用到）

### 1-2. 準備 Docker Image 並推上 ECR

```bash
# 進入 backend 目錄
cd /path/to/exchange/backend

# 建立 ECR repository
aws ecr create-repository \
  --repository-name exchange-backend \
  --region ap-northeast-1

# 取得 ECR registry URL（記下這個值）
ECR_URL=$(aws ecr describe-repositories \
  --repository-names exchange-backend \
  --query 'repositories[0].repositoryUri' \
  --output text)
echo $ECR_URL

# Login Docker 到 ECR
aws ecr get-login-password --region ap-northeast-1 | \
  docker login --username AWS --password-stdin $ECR_URL

# Build & Push
docker build -t exchange-backend .
docker tag exchange-backend:latest $ECR_URL:latest
docker push $ECR_URL:latest
```

> **Dockerfile 注意**：確認 `Dockerfile` 最後的 `CMD` 是跑 `./server` 或 `cmd/server/main.go`，並且 `EXPOSE 8080`。

### 1-3. 用 Terraform 開機

```bash
cd infra/terraform/quick-deploy

# 複製設定檔
cp terraform.tfvars.example terraform.tfvars

# 填入你的 IP（只允許你自己的 IP SSH 進去）
MY_IP=$(curl -s ifconfig.me)
sed -i '' "s|YOUR_IP_HERE|$MY_IP|g" terraform.tfvars

# 初始化並部署（約 2-3 分鐘）
terraform init
terraform apply

# 輸出會顯示
# instance_public_ip = "x.x.x.x"
# ssh_command        = "ssh -i ~/.ssh/id_rsa ec2-user@x.x.x.x"
# portainer_url      = "http://x.x.x.x:9000"
# api_url            = "http://x.x.x.x:8080"
```

### 1-4. 進 Portainer 部署 Backend

1. 打開 `http://<EC2_IP>:9000`，第一次登入需要設定 admin 密碼
2. 選 `Get Started` → `local` 環境
3. 進 **Containers** → 看到 `exchange-postgres` 已在運行
4. 點 **+ Add Container**：
   - Image：`<ECR_URL>:latest`（你的 ECR image）
   - Port mapping：`8080:8080`
   - Environment：
     ```
     DATABASE_URL=postgres://postgres:Exchange123!@exchange-postgres:5432/exchange?sslmode=disable
     GIN_MODE=release
     ```
   - Network：選 `exchange_default`（跟 postgres 同網路）
5. Deploy Container

### 1-5. 初始化雲端資料庫

```bash
# SSH 進 EC2
ssh -i ~/.ssh/id_rsa ec2-user@<EC2_IP>

# 進 postgres container 執行 schema
docker exec -i exchange-postgres psql -U postgres -d exchange < /dev/stdin << 'EOF'
-- 貼入 sql/schema.sql 的內容，或用下面的方式
EOF

# 更好的方式：直接從本機傳
exit  # 先退出 SSH

# 從本機把 sql 檔塞進去
cat sql/schema.sql | ssh -i ~/.ssh/id_rsa ec2-user@<EC2_IP> \
  "docker exec -i exchange-postgres psql -U postgres -d exchange"

cat sql/seed.sql | ssh -i ~/.ssh/id_rsa ec2-user@<EC2_IP> \
  "docker exec -i exchange-postgres psql -U postgres -d exchange"
```

### 1-6. 驗證雲端部署

```bash
EC2_IP="<你的 EC2 IP>"

# 健康檢查
curl http://$EC2_IP:8080/api/v1/orderbook?symbol=BTCUSDT

# 下單測試
curl -X POST http://$EC2_IP:8080/api/v1/orders \
  -H "Content-Type: application/json" \
  -d '{"user_id":1,"symbol":"BTCUSDT","side":"buy","type":"limit","price":"40000","quantity":"0.01"}'
```

**成就解鎖：你的 API 現在有真實的 IP，全世界都能打。**

---

## Phase 2：第一次壓測，找到瓶頸

### 2-1. 安裝 k6

```bash
brew install k6
```

### 2-2. 建立壓測腳本

建立 `backend/tests/load/k6_order_stress.js`：

```javascript
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';

// 自訂指標
const orderLatency  = new Trend('order_latency_ms');
const orderbookLatency = new Trend('orderbook_latency_ms');
const errorRate     = new Rate('error_rate');
const totalOrders   = new Counter('total_orders');

// ── 測試設定 ──────────────────────────────────────────────────
export const options = {
  stages: [
    { duration: '30s', target: 10  },  // 暖身
    { duration: '1m',  target: 50  },  // 基準線
    { duration: '30s', target: 100 },  // 施壓
    { duration: '2m',  target: 100 },  // 持續觀察
    { duration: '30s', target: 200 },  // 繼續加壓
    { duration: '1m',  target: 200 },  // 觀察崩潰點
    { duration: '30s', target: 0   },  // 收尾
  ],
  thresholds: {
    // 這是你的 SLA，壓測若超過會標記 FAIL
    'order_latency_ms':     ['p(95)<500'],   // 下單 P95 < 500ms
    'orderbook_latency_ms': ['p(95)<100'],   // 訂單簿查詢 P95 < 100ms
    'error_rate':           ['rate<0.01'],   // 錯誤率 < 1%
    'http_req_duration':    ['p(99)<2000'],  // 全局 P99 < 2s
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// VU 執行的主邏輯
export default function() {
  // 70% 機率查詢訂單簿（讀多寫少的真實比例）
  if (Math.random() < 0.7) {
    const start = Date.now();
    const res = http.get(`${BASE_URL}/api/v1/orderbook?symbol=BTCUSDT`);
    orderbookLatency.add(Date.now() - start);
    check(res, { 'orderbook 200': (r) => r.status === 200 });
  } else {
    // 30% 下單
    const side  = Math.random() > 0.5 ? 'buy' : 'sell';
    const price = (39000 + Math.random() * 3000).toFixed(2);
    const qty   = (0.001 + Math.random() * 0.05).toFixed(4);

    const start = Date.now();
    const res   = http.post(
      `${BASE_URL}/api/v1/orders`,
      JSON.stringify({
        user_id:  1,
        symbol:   'BTCUSDT',
        side:     side,
        type:     'limit',
        price:    price,
        quantity: qty,
      }),
      { headers: { 'Content-Type': 'application/json' } }
    );
    const latency = Date.now() - start;

    orderLatency.add(latency);
    errorRate.add(res.status !== 200 && res.status !== 201);
    totalOrders.add(1);

    check(res, {
      'order placed':   (r) => r.status === 200 || r.status === 201,
      'has order id':   (r) => JSON.parse(r.body)?.id !== undefined,
    });
  }

  sleep(0.1);  // 每個 VU 每 100ms 一個請求
}
```

### 2-3. 執行壓測

```bash
# 打本地測試（先確認腳本正確）
k6 run --vus 5 --duration 10s \
  -e BASE_URL=http://localhost:8080 \
  backend/tests/load/k6_order_stress.js

# 打 AWS（正式壓測）
k6 run \
  -e BASE_URL=http://<EC2_IP>:8080 \
  backend/tests/load/k6_order_stress.js

# 輸出結果到 JSON（等等拿來對比用）
k6 run \
  --out json=backend/tests/load/results/phase1_baseline.json \
  -e BASE_URL=http://<EC2_IP>:8080 \
  backend/tests/load/k6_order_stress.js
```

### 2-4. 同時監控 EC2 與 PostgreSQL

**開另一個 terminal，SSH 進 EC2：**

```bash
ssh -i ~/.ssh/id_rsa ec2-user@<EC2_IP>

# 持續監控 container 資源用量
watch -n 2 'docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}"'
```

**觀察 PostgreSQL 連線數（壓測期間最重要的指標）：**

```bash
# 在另一個 terminal
docker exec exchange-postgres psql -U postgres -d exchange \
  -c "SELECT count(*), state FROM pg_stat_activity WHERE datname='exchange' GROUP BY state;"

# 每 3 秒刷新
watch -n 3 'docker exec exchange-postgres psql -U postgres -d exchange \
  -c "SELECT count(*) as total, state FROM pg_stat_activity WHERE datname='"'"'exchange'"'"' GROUP BY state;"'
```

### 2-5. 你會觀察到的崩潰模式

```
VU: 10  →  P95: ~50ms    ✅ 一切正常，DB 連線數 < 20
VU: 50  →  P95: ~200ms   ⚠️  開始慢，DB 連線數 ~60
VU: 100 →  P95: ~1500ms  ❌ DB 連線池快滿，orderbook 查詢拖累下單
VU: 200 →  error_rate 爆  ❌ "sorry, too many clients already"
```

**k6 summary 的關鍵數字：**
```
order_latency_ms.........: avg=856ms  p(95)=3200ms  ← 遠超 SLA 的 500ms
error_rate...............: 12.3%                    ← 超過 1% 上限
total_orders.............: 3821
```

**記錄下這些數字，這是你的「基準線」。後面每次架構升級，都要跟這個比。**

---

## 決策點 A：換機器 vs 調參數 vs 加快取

**黃金原則：先調參數，再換機器，最後改架構。花錢升級之前，先把便宜的方案試完。**

### 判斷流程

```
壓測出現問題
      │
      ▼
EC2 CPU 使用率？
      │
  ┌───┴────┐
< 60%     > 80%
  │          │
  ▼          ▼
DB 是瓶頸   Go 是瓶頸
  │          │
  ▼          ▼
先調 DB    先調 Go
參數       連線池
```

### Step A1：調 Go 連線池（免費，改程式碼）

找到 `internal/repository/postgres.go` 的 DB 初始化：

```go
// 調整 pgxpool 連線池設定
config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
config.MaxConns          = 30   // 預設可能只有 4！
config.MinConns          = 5
config.MaxConnLifetime   = time.Hour
config.MaxConnIdleTime   = 30 * time.Minute
config.HealthCheckPeriod = time.Minute
pool, err := pgxpool.NewWithConfig(ctx, config)
```

重新 build & push，在 Portainer 重啟 container，再壓一次，對比數字。

### Step A2：調 PostgreSQL 參數（免費，改 Docker 啟動命令）

```bash
# 停掉舊的 postgres，用更好的設定重啟
docker stop exchange-postgres && docker rm exchange-postgres

docker run -d --name exchange-postgres \
  --network exchange_default \
  -e POSTGRES_PASSWORD=Exchange123! \
  -e POSTGRES_DB=exchange \
  -v postgres_data:/var/lib/postgresql/data \
  -p 5432:5432 \
  postgres:15-alpine \
  postgres \
    -c max_connections=200 \
    -c shared_buffers=256MB \
    -c effective_cache_size=768MB \
    -c work_mem=4MB \
    -c checkpoint_completion_target=0.9 \
    -c wal_buffers=16MB

# 重啟後要重跑 schema（因為 volume 有資料，其實不用）
```

### Step A3：決定是否換機器

| 觀察到的指標 | 動作 |
|---|---|
| EC2 CPU 持續 > 80% | `terraform apply` 改 `instance_type = "t3.large"` |
| Go 記憶體用量接近 4GB | 換 t3.large，或檢查 memory leak |
| DB CPU < 60% 但 latency 高 | 可能是 N+1 Query，開 `pg_stat_statements` 查慢查詢 |
| DB 連線數滿 + CPU > 70% | 進入 Phase 3，加 Redis |

**垂直擴展的代價：**
- t3.medium → t3.large：+$0.04/hr (~$30/月)
- 通常 `terraform apply` 改一個參數，5 分鐘內完成

---

## Phase 3：引入 Redis

**觸發條件**：`GET /orderbook` 的 QPS 佔了 70% 流量，但每次都打到 DB，形成讀取熱點。

### 3-1. 在 EC2 上加 Redis Container

```bash
ssh -i ~/.ssh/id_rsa ec2-user@<EC2_IP>

# 加一個 Redis 到同一個 Docker network
docker run -d --name exchange-redis \
  --network exchange_default \
  --restart always \
  -p 6379:6379 \
  redis:7-alpine \
  redis-server --maxmemory 512mb --maxmemory-policy allkeys-lru
```

### 3-2. 加 Redis 依賴到 Go 專案

```bash
# 在本機的 backend 目錄
go get github.com/redis/go-redis/v9
```

### 3-3. 建立 Cache 層

建立 `internal/infrastructure/cache/redis_cache.go`：

```go
package cache

import (
    "context"
    "encoding/json"
    "time"

    "github.com/redis/go-redis/v9"
)

type RedisCache struct {
    client *redis.Client
}

func NewRedisCache(addr string) *RedisCache {
    return &RedisCache{
        client: redis.NewClient(&redis.Options{
            Addr:         addr, // "exchange-redis:6379"
            PoolSize:     50,
            MinIdleConns: 5,
            DialTimeout:  5 * time.Second,
            ReadTimeout:  3 * time.Second,
            WriteTimeout: 3 * time.Second,
        }),
    }
}

// Ping 確認 Redis 連線
func (c *RedisCache) Ping(ctx context.Context) error {
    return c.client.Ping(ctx).Err()
}

// GetJSON 取得快取的 JSON 資料
func (c *RedisCache) GetJSON(ctx context.Context, key string, dest any) (bool, error) {
    data, err := c.client.Get(ctx, key).Bytes()
    if err == redis.Nil {
        return false, nil // cache miss
    }
    if err != nil {
        return false, err
    }
    return true, json.Unmarshal(data, dest)
}

// SetJSON 將資料存入快取
func (c *RedisCache) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
    data, err := json.Marshal(value)
    if err != nil {
        return err
    }
    return c.client.Set(ctx, key, data, ttl).Err()
}

// Delete 讓快取失效
func (c *RedisCache) Delete(ctx context.Context, keys ...string) error {
    return c.client.Del(ctx, keys...).Err()
}
```

### 3-4. 在 Handler 加入 Cache-Aside 邏輯

在 `internal/api/handlers.go` 的訂單簿 handler：

```go
// GetOrderBook handler（改寫，加入快取）
func (h *Handler) GetOrderBook(c *gin.Context) {
    symbol := strings.ToUpper(c.Query("symbol"))
    cacheKey := "orderbook:" + symbol

    // 1. 先查 Redis
    var snapshot OrderBookResponse
    hit, err := h.cache.GetJSON(c.Request.Context(), cacheKey, &snapshot)
    if err == nil && hit {
        c.Header("X-Cache", "HIT")  // 方便 debug 時看是否走快取
        c.JSON(http.StatusOK, snapshot)
        return
    }

    // 2. Cache Miss，查 DB（原本的邏輯）
    result, err := h.service.GetOrderBook(c.Request.Context(), symbol)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    // 3. 存回 Redis，TTL 設 500ms（訂單簿變化快，不能太久）
    _ = h.cache.SetJSON(c.Request.Context(), cacheKey, result, 500*time.Millisecond)

    c.Header("X-Cache", "MISS")
    c.JSON(http.StatusOK, result)
}
```

在下單成功後，讓快取失效：

```go
// PlaceOrder handler（成交後讓訂單簿快取失效）
func (h *Handler) PlaceOrder(c *gin.Context) {
    // ... 原本的下單邏輯 ...

    if err := h.service.PlaceOrder(ctx, &order); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // 讓訂單簿快取失效，下次查詢會拿最新資料
    _ = h.cache.Delete(c.Request.Context(), "orderbook:"+order.Symbol)

    c.JSON(http.StatusCreated, order)
}
```

### 3-5. 壓測對比

```bash
# build 新的 image，push，Portainer 重啟
docker build -t exchange-backend .
docker tag exchange-backend:latest $ECR_URL:latest
docker push $ECR_URL:latest
# → 在 Portainer 重新 pull 並重啟 exchange-backend container

# 再跑一次壓測，存成新結果
k6 run \
  --out json=backend/tests/load/results/phase3_with_redis.json \
  -e BASE_URL=http://<EC2_IP>:8080 \
  backend/tests/load/k6_order_stress.js
```

**預期改善：**
- `GET /orderbook` 的 P95：200ms → 5ms
- PostgreSQL CPU：70% → 25%
- 整體可承受的 VU 數量：提升 2-4 倍

---

## Phase 4：遷移到 ECS，水平擴展

**觸發條件**：調完參數、加了 Redis，單機 EC2 的 CPU 還是在 80% 以上；或者你想學 ECS 的運作方式。

### 4-1. ECS 架構概覽

```
Internet
    │
    ▼
ALB (Application Load Balancer)
    │              │
    ▼              ▼
ECS Task 1     ECS Task 2     ← Fargate，不用管 Server，按用量付費
(Go backend)   (Go backend)
    │              │
    └──────┬───────┘
           ▼
         Redis (還是在 EC2，或換 ElastiCache)
           │
           ▼
         PostgreSQL (還是在 EC2，或換 RDS)
```

### 4-2. 建立 ECS 資源（用現有的 terraform 擴充）

`backend/infra/terraform/` 裡已有 `ecs.tf`，確認並補充以下設定：

```hcl
# backend/infra/terraform/ecs.tf 補充片段

resource "aws_ecs_cluster" "main" {
  name = "exchange-cluster"

  setting {
    name  = "containerInsights"
    value = "enabled"  # 開啟 CloudWatch Container Insights
  }
}

resource "aws_ecs_task_definition" "backend" {
  family                   = "exchange-backend"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "512"   # 0.5 vCPU
  memory                   = "1024"  # 1 GB

  container_definitions = jsonencode([{
    name      = "backend"
    image     = "${var.ecr_url}:latest"
    essential = true
    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]
    environment = [
      { name = "GIN_MODE",      value = "release" },
      { name = "DATABASE_URL",  value = var.database_url },
      { name = "REDIS_URL",     value = var.redis_url },
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = "/ecs/exchange-backend"
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])
}

resource "aws_ecs_service" "backend" {
  name            = "exchange-backend"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.backend.arn
  desired_count   = 2  # 從 2 個副本開始
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.ecs_tasks.id]
    assign_public_ip = false  # 放在 private subnet，透過 ALB 對外
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.backend.arn
    container_name   = "backend"
    container_port   = 8080
  }

  # Auto Scaling 設定（進階）
  lifecycle {
    ignore_changes = [desired_count]  # 讓 Auto Scaling 管理數量
  }
}

# Auto Scaling Policy
resource "aws_appautoscaling_target" "ecs_target" {
  max_capacity       = 10
  min_capacity       = 2
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.backend.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "scale_up" {
  name               = "exchange-scale-up"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.ecs_target.resource_id
  scalable_dimension = aws_appautoscaling_target.ecs_target.scalable_dimension
  service_namespace  = aws_appautoscaling_target.ecs_target.service_namespace

  target_tracking_scaling_policy_configuration {
    target_value = 70.0  # CPU 超過 70% 就 scale out
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    scale_in_cooldown  = 300  # Scale in 前等 5 分鐘
    scale_out_cooldown = 60   # Scale out 前等 1 分鐘
  }
}
```

### 4-3. 你會立刻遇到的 Race Condition

當你有 2 個 ECS Task，用 k6 打高並發下單，觀察帳戶餘額：

```
用戶 A 餘額: 100 USDT
請求 1 → Task 1: SELECT balance=100, 下買單 80 USDT
請求 2 → Task 2: SELECT balance=100, 下買單 90 USDT（幾乎同時）
兩筆都成功 ← 因為都讀到 100！
餘額: 100 - 80 - 90 = -70 USDT  ❌ 超賣了
```

**這就是分散式系統的核心問題。**

---

## 決策點 B：Race Condition 怎麼辦

### B1：PostgreSQL Row-level Lock（推薦，最簡單）

你的 `postgres.go` 裡 `LockFunds` 必須是這樣的原子操作：

```sql
-- 這個 SQL 天生就是安全的，PostgreSQL 的行鎖保證原子性
UPDATE accounts
SET
  locked_balance    = locked_balance + $1,
  available_balance = available_balance - $1
WHERE
  user_id  = $2
  AND currency = $3
  AND available_balance >= $1  -- ← 這個條件是關鍵，餘額不足就 UPDATE 0 rows
RETURNING id;

-- 在 Go 裡檢查影響的 row 數
-- 如果 rowsAffected == 0，代表餘額不足或 race，回傳錯誤
```

**驗證方法：**

```bash
# 建立一個 race condition 壓測腳本
# 讓同一個 user 同時發出超過餘額的下單
k6 run backend/tests/load/k6_race_test.js
```

```javascript
// backend/tests/load/k6_race_test.js
// 同一個 user_id，同時打 100 個下單，每筆金額是總餘額的 90%
// 正確行為：只有一筆成功，其餘都應該收到 "餘額不足" 錯誤
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  vus: 100,
  iterations: 100,  // 每個 VU 各 1 次，共 100 個同時請求
};

export default function() {
  const res = http.post(
    `${__ENV.BASE_URL}/api/v1/orders`,
    JSON.stringify({
      user_id: 1,        // 同一個用戶
      symbol: 'BTCUSDT',
      side: 'buy',
      type: 'limit',
      price: '40000',
      quantity: '2.25',  // 假設用戶只有 1 BTC 可鎖定
    }),
    { headers: { 'Content-Type': 'application/json' } }
  );

  // 應該只有 1 筆成功，99 筆都是 400 "餘額不足"
  check(res, {
    'is 200 or 400': (r) => r.status === 200 || r.status === 400,
    'NOT 500':       (r) => r.status !== 500,  // 500 代表有 bug
  });
}
```

### B2：撮合引擎的 Race Condition（架構問題）

撮合引擎是全記憶體的，多個 ECS Task 各自維護一份訂單簿，這是根本性的架構問題。

**解法選項：**

| 方案 | 實作難度 | 效能 | 適合場景 |
|---|---|---|---|
| ALB Sticky Session | ⭐ 簡單 | ✅ 好 | 驗證用，不推薦長期 |
| Redis 共享訂單簿 | ⭐⭐ 中等 | ⚠️ OK | 中等吞吐量 |
| 撮合引擎獨立服務（只跑 1 個） | ⭐⭐⭐ 複雜 | ✅ 最好 | 生產環境 |

**學習階段的快速解法（Sticky Session）：**

```hcl
# 在 ALB Target Group 開啟 Stickiness
resource "aws_lb_target_group" "backend" {
  # ...
  stickiness {
    type            = "lb_cookie"
    cookie_duration = 86400  # 1 天
    enabled         = true
  }
}
```

**Sticky Session 的缺點（你要知道）：** 某個 Task 掛掉，那個用戶的訂單簿狀態消失。這就是為什麼生產環境要把撮合引擎拆出去（見 Phase 7）。

---

## Phase 5：引入 Kafka，異步削峰

**觸發條件**：k6 壓到 VU=300 時，`POST /orders` 開始大量 timeout，Go log 出現 `context deadline exceeded`。

### 5-1. 問題根源分析

現在的下單是同步的，整條鏈都阻塞在 HTTP request 裡：

```
HTTP 進來 → 鎖資金(DB) → 建訂單(DB) → 撮合 → 更新狀態(DB) → 回應
                                                               ↑ 用戶等這個
```

每一步都有 latency，加起來在高並發下就爆了。

### 5-2. 在 EC2 上加 Kafka（用 Redpanda，更輕量）

```bash
ssh ec2-user@<EC2_IP>

docker run -d --name exchange-redpanda \
  --network exchange_default \
  --restart always \
  -p 9092:9092 \
  -p 9644:9644 \
  docker.redpanda.com/redpandadata/redpanda:latest \
  redpanda start \
    --overprovisioned \
    --smp 1 \
    --memory 512M \
    --reserve-memory 0M \
    --node-id 0 \
    --kafka-addr PLAINTEXT://0.0.0.0:9092 \
    --advertise-kafka-addr PLAINTEXT://exchange-redpanda:9092

# 建立 topic
docker exec exchange-redpanda rpk topic create orders --partitions 3
docker exec exchange-redpanda rpk topic create order-results --partitions 3
```

### 5-3. 改造下單流程

**新架構：**
```
HTTP 進來 → 基本驗證 → 丟進 Kafka → 立刻回 202 Accepted + order_id
                                          ↑ 用戶收到這個，不用等成交

【背景 Worker】
Kafka Consumer → 鎖資金 → 撮合 → 更新狀態 → WebSocket 推送結果給用戶
```

**新增 Kafka producer 到 handler（簡化版）：**

```go
// internal/api/handlers.go
func (h *Handler) PlaceOrderAsync(c *gin.Context) {
    var req PlaceOrderRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // 產生 order ID（先給用戶，讓他可以追蹤）
    orderID := uuid.New()

    // 丟進 Kafka（非常快，microseconds）
    msg := OrderMessage{
        OrderID:  orderID,
        UserID:   req.UserID,
        Symbol:   req.Symbol,
        Side:     req.Side,
        Type:     req.Type,
        Price:    req.Price,
        Quantity: req.Quantity,
    }

    if err := h.producer.SendOrder(c.Request.Context(), msg); err != nil {
        c.JSON(http.StatusServiceUnavailable, gin.H{"error": "queue unavailable"})
        return
    }

    // 立刻回傳 202，不等撮合結果
    c.JSON(http.StatusAccepted, gin.H{
        "order_id": orderID,
        "status":   "pending",
        "message":  "Order queued, result will be pushed via WebSocket",
    })
}
```

**背景 Worker（新的 cmd/worker/main.go）：**

```go
// cmd/worker/main.go
func main() {
    // 消費 Kafka orders topic
    consumer := kafka.NewConsumer("exchange-redpanda:9092", "order-processor")

    for msg := range consumer.Messages("orders") {
        var order OrderMessage
        json.Unmarshal(msg.Value, &order)

        // 這裡執行原本的同步邏輯（鎖資金、撮合、更新狀態）
        result, err := service.ProcessOrder(ctx, order)

        // 透過 WebSocket 推送結果給用戶
        wsHub.SendToUser(order.UserID, result)

        // Commit offset（告訴 Kafka 這條消息已處理）
        consumer.Commit(msg)
    }
}
```

### 5-4. 壓測對比

```bash
k6 run \
  --out json=backend/tests/load/results/phase5_with_kafka.json \
  -e BASE_URL=http://<EC2_IP>:8080 \
  backend/tests/load/k6_order_stress.js
```

**預期改善：**
- `POST /orders` P95：1500ms → 30ms（因為只是丟 Kafka）
- 可承受 VU：200 → 1000+
- 代價：用戶要透過 WebSocket 接收成交結果，複雜度增加

### 5-5. 你要理解的副作用（面試考點）

| 問題 | 說明 |
|---|---|
| **最終一致性** | 用戶下單後，訂單「終將」成交，但不是立刻 |
| **at-least-once delivery** | Kafka 保證訊息至少送達一次，所以 Worker 要做冪等處理 |
| **Consumer lag** | Worker 處理速度 < 生產速度時，lag 會增加，訂單延遲變高 |
| **如何處理 Worker 掛掉** | Kafka 的 offset 機制確保重啟後從斷點繼續，不掉單 |

---

## Phase 6：可觀測性

**沒有監控，壓測是盲人摸象。** 這是大廠最看重的技能之一。

### 6-1. 在 Go 程式中埋點

加 Prometheus metrics 依賴：

```bash
go get github.com/prometheus/client_golang/prometheus
go get github.com/prometheus/client_golang/prometheus/promauto
go get github.com/prometheus/client_golang/prometheus/promhttp
```

建立 `internal/infrastructure/metrics/metrics.go`：

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // 下單延遲（Histogram）→ 可以看 P50/P95/P99
    OrderDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "exchange_order_duration_seconds",
            Help:    "End-to-end order processing duration",
            Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
        },
        []string{"status"},  // labels: success / failed
    )

    // 訂單簿深度（Gauge）→ 即時值
    OrderBookDepth = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "exchange_orderbook_depth_total",
            Help: "Number of orders in the order book",
        },
        []string{"symbol", "side"},  // labels: BTCUSDT, buy/sell
    )

    // HTTP 錯誤數（Counter）→ 只增不減
    HttpErrors = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "exchange_http_errors_total",
            Help: "Total number of HTTP errors",
        },
        []string{"method", "path", "status_code"},
    )

    // Redis 快取命中率（Counter）
    CacheHits = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "exchange_cache_operations_total",
            Help: "Cache hit/miss counters",
        },
        []string{"result"},  // labels: hit / miss
    )

    // Kafka Consumer Lag（Gauge）
    KafkaConsumerLag = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "exchange_kafka_consumer_lag",
            Help: "Kafka consumer lag per partition",
        },
        []string{"topic", "partition"},
    )
)
```

在 `cmd/server/main.go` 暴露 metrics endpoint：

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

// 在 router 設定中加入
router.GET("/metrics", gin.WrapH(promhttp.Handler()))
```

### 6-2. 部署 Prometheus + Grafana（在 EC2 上）

```bash
ssh ec2-user@<EC2_IP>

# Prometheus
docker run -d --name prometheus \
  --network exchange_default \
  --restart always \
  -p 9090:9090 \
  -v /opt/exchange/prometheus.yml:/etc/prometheus/prometheus.yml \
  prom/prometheus:latest

# Grafana
docker run -d --name grafana \
  --network exchange_default \
  --restart always \
  -p 3001:3000 \
  -e GF_SECURITY_ADMIN_PASSWORD=monitor123 \
  -v grafana_data:/var/lib/grafana \
  grafana/grafana:latest
```

在 EC2 上建立 `/opt/exchange/prometheus.yml`：

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'exchange-backend'
    static_configs:
      - targets: ['exchange-backend:8080']
    metrics_path: '/metrics'

  - job_name: 'postgres-exporter'
    static_configs:
      - targets: ['postgres-exporter:9187']

  - job_name: 'redis-exporter'
    static_configs:
      - targets: ['redis-exporter:9121']
```

> Security Group 要開 3001 port（限你的 IP）才能進 Grafana。

### 6-3. 匯入 Grafana Dashboard

1. 打開 `http://<EC2_IP>:3001`，登入（admin/monitor123）
2. **Dashboards** → **Import**，輸入以下 Dashboard ID：
   - `1860`：Node Exporter Full（EC2 系統指標）
   - `9628`：PostgreSQL Database
   - `11835`：Redis Dashboard
3. 自己建一個 **Exchange** Dashboard，加入這些 Panel：
   - `rate(exchange_order_duration_seconds_count[1m])` → 下單 TPS
   - `histogram_quantile(0.95, exchange_order_duration_seconds_bucket)` → 下單 P95
   - `exchange_orderbook_depth_total` → 訂單簿深度
   - `rate(exchange_http_errors_total[1m])` → 錯誤率

### 6-4. 壓測時盯著 Grafana 說出瓶頸

**這是最終目標**：看著 Grafana 的圖，直接說出：

```
「現在 VU 加到 200，P95 飆到 2 秒，
 但 Go 的 CPU 只有 40%，
 PostreSQL 的 active connections 卡在 98/100，
 而且 exchange_kafka_consumer_lag 在上升，
 所以瓶頸是 DB 連線池，不是 Go 服務本身。
 解法：調大 max_connections，或者加 PgBouncer 連線池代理。」
```

**能說出這句話，面試就贏了。**

---

## 決策點 C：何時拆微服務

### 不拆的理由（先確認這些都達到了）

- [ ] 單體在 ECS 2 個 Task 下，壓測 VU=500 還撐得住
- [ ] 你能清楚說出每個服務的瓶頸在哪裡
- [ ] 你理解了 Kafka 的最終一致性

### 拆的時機（量化門檻）

| 觸發條件 | 對應的拆分決定 |
|---|---|
| 撮合引擎 CPU > 70%，但 API handler 只有 20% | 撮合引擎獨立成一個 ECS Service |
| `GET /orderbook` QPS 是 `POST /orders` 的 100 倍 | 獨立 Market Data Service，可以無狀態 Scale Out |
| 部署新功能需要停整個服務（含撮合引擎）| 按業務邊界拆，讓彼此獨立部署 |

### 你的專案的自然拆分路徑

`backend/cmd/` 已經預留了拆分入口：

```
目前：cmd/server/main.go  (全部在一起)
         │
         ▼
Phase 7A：
  cmd/server/main.go        → API Gateway（只做路由、驗證、限流）
  cmd/matching-engine/      → 撮合引擎（單一實例，全記憶體，高優先級 CPU）
  cmd/order-service/        → 訂單狀態管理（多實例，無狀態）
```

每個服務變成獨立的 ECS Service，各自 Scale，各自部署，這就是微服務的實際形態。

---

## 費用控制與環境清理

### 每次實驗完要做的事

```bash
# 關掉 EC2（節省約 $0.05/hr）
cd backend/infra/terraform/quick-deploy
terraform destroy

# 確認全部刪除
aws ec2 describe-instances \
  --filters "Name=tag:Name,Values=exchange-test" \
  --query 'Reservations[].Instances[].State.Name'
# 應該回傳 "terminated"
```

### 費用估算總表

| 場景 | 架構 | 預估月費 |
|---|---|---|
| Phase 1-3：EC2 單機 | t3.medium + 自管 PG/Redis | ~$35/月 |
| Phase 4：ECS + ALB | 2x Fargate(0.5vCPU) + ALB | ~$60/月 |
| Phase 4：ECS + RDS | 上面 + RDS db.t3.micro | ~$90/月 |
| Phase 4：ECS + ElastiCache | 再加 cache.t3.micro | ~$115/月 |
| Phase 5：加 MSK Kafka | 再加 MSK t3.small x2 | ~$195/月 |

**建議：Phase 1-3 用 EC2 自管全部（最省），Phase 4 才考慮換 RDS/ElastiCache。**

### 省錢技巧

```bash
# 用 Spot Instance 做壓測（便宜 70%，但可能被中斷）
# 在 variables.tf 加：
# spot_price = "0.03"  # 最高出價

# 壓測完立刻 destroy，不要讓 EC2 閒置
# 設定 AWS Billing Alert（超過 $50 就發 email）
aws cloudwatch put-metric-alarm \
  --alarm-name "BillingAlert-50USD" \
  --alarm-description "Alert when AWS monthly bill exceeds $50" \
  --metric-name EstimatedCharges \
  --namespace AWS/Billing \
  --statistic Maximum \
  --period 86400 \
  --threshold 50 \
  --comparison-operator GreaterThanThreshold \
  --dimensions Name=Currency,Value=USD \
  --evaluation-periods 1 \
  --alarm-actions arn:aws:sns:us-east-1:<account-id>:<your-sns-topic>
```

---

## 學習產出對照表

每個 Phase 結束，你應該能清楚回答這些問題：

| Phase | 問自己 |
|---|---|
| Phase 1 | 「EC2 Security Group、ECR、VPC 是什麼關係？為什麼 EC2 能讀 ECR？」|
| Phase 2 | 「我的系統在多少 VU 下開始 degrade？瓶頸在哪一層？」|
| Phase 3 | 「Cache-Aside 和 Write-Through 的差異？TTL 要怎麼設？快取失效的時機？」|
| Phase 4 | 「ECS Fargate 和 EC2 Launch Type 的差異？為什麼要用 ALB？」|
| 決策點 B | 「為什麼 PostgreSQL Row-level Lock 可以防止超賣？Redlock 適合什麼場景？」|
| Phase 5 | 「at-least-once delivery 和 exactly-once 的代價是什麼？Consumer Group 的作用？」|
| Phase 6 | 「Histogram 和 Counter 的使用場景？P95 和 P99 代表什麼？」|
| Phase 7 | 「你會根據什麼數據決定拆微服務？拆太早有什麼代價？」|
