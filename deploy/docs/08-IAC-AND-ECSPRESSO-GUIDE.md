# IaC 完整教學指南 — Terraform × ecspresso × AWS ECS

> 本文件面向「第一次接觸這份 repo 的雲端部署設定」的讀者，目的是讓你看完之後能獨立操作整個部署流程，並且知道當出錯時要去哪裡找問題。

---

## 目錄

1. [先搞清楚兩個工具的分工](#1-先搞清楚兩個工具的分工)
2. [整體架構鳥瞰圖](#2-整體架構鳥瞰圖)
3. [Terraform 詳解：每個 module 做什麼](#3-terraform-詳解每個-module-做什麼)
4. [ecspresso 詳解：ECS 服務部署如何運作](#4-ecspresso-詳解ecs-服務部署如何運作)
5. [環境變數全覽：must_env 每個值從哪裡來](#5-環境變數全覽must_env-每個值從哪裡來)
6. [部署順序與依賴關係](#6-部署順序與依賴關係)
7. [常見錯誤與排除方法](#7-常見錯誤與排除方法)

---

## 1. 先搞清楚兩個工具的分工

這份 repo 用了兩個工具部署到 AWS，很多人第一次看到會搞混。

### Terraform 負責：基礎設施（infrastructure）

Terraform 建立的是「不常動、多個服務共用」的底層資源：

| 建立的 AWS 資源 | 說明 |
|---|---|
| VPC、子網路、NAT Gateway | 網路隔離環境，讓服務安全運行 |
| 安全群組（Security Group） | 網路防火牆規則，決定誰能連誰 |
| ALB（Application Load Balancer） | 對外唯一入口，把外部流量導到 gateway |
| ECS Cluster | 所有 ECS 服務的容器執行環境 |
| ECR Repository | 存放 Docker image 的倉庫 |
| IAM Role | ECS 容器執行時的 AWS 權限 |
| RDS PostgreSQL | 主資料庫 |
| ElastiCache Redis | 快取 / 限流 / WebSocket 訂閱 |
| Redpanda（ECS Service） | Kafka 相容的訊息佇列（Terraform 直接管理） |
| EFS（Elastic File System） | Redpanda 的資料持久化儲存空間 |
| Cloud Map（Service Discovery） | 讓服務之間透過 DNS 互相找到對方 |
| SSM Parameter Store | 儲存連線字串等敏感設定 |
| CloudWatch Log Groups | 收集容器日誌 |
| AWS Budgets | 當月費用超標自動寄信警報 |

### ecspresso 負責：應用服務本身（application services）

ecspresso 管理的是「每次 code 有改動就要更新」的四個微服務：

| ECS Service | 說明 |
|---|---|
| `gateway` | 對外 API 入口，反向代理其他服務 |
| `order-service` | 訂單建立、查詢、撮合事件發布 |
| `matching-engine` | 消費 Kafka 事件，執行撮合邏輯 |
| `market-data-service` | 消費撮合結果，維護行情，推播 WebSocket |

**為什麼不讓 Terraform 管理這四個服務？**

因為這四個服務每次發版（更新 Docker image）都需要更新，頻率遠高於基礎設施。如果讓 Terraform 管，你每次發版就要跑一次 `terraform apply`，這樣又慢又有風險（terraform apply 可能誤動其他資源）。ecspresso 只動 Task Definition 和 Service，速度快、範圍小、安全。

---

## 2. 整體架構鳥瞰圖

```
外部使用者
    │
    ▼ HTTP :80
┌─────────────────────────────────────────────────────────────┐
│  ALB（Application Load Balancer）                           │
│  ‣ 對外開放 port 80                                          │
│  ‣ 健康檢查 gateway /health                                  │
└─────────────────────────────────────────────────────────────┘
    │
    ▼ :8100 (gateway Target Group)
┌─────────────────────────────────────────────────────────────┐
│  gateway                                                    │
│  ECS Fargate — exchange-staging cluster                     │
│  ‣ 反向代理 order-service / market-data-service / simulation │
│  ‣ 讀取 Redis（WebSocket 限流、session）                     │
│  ‣ 透過 Cloud Map DNS 找其他服務                             │
└─────────────────────────────────────────────────────────────┘
    │                      │
    ▼ :8103                ▼ :8102
┌───────────────┐  ┌──────────────────────┐
│ order-service │  │ market-data-service  │
│ ‣ 接收訂單    │  │ ‣ 訂閱撮合結果        │
│ ‣ 寫入 DB     │  │ ‣ 維護 orderbook     │
│ ‣ 發布 Kafka  │  │ ‣ 推播 WebSocket     │
└───────────────┘  └──────────────────────┘
    │                      ▲
    ▼ Kafka topic          │ Kafka topic
┌─────────────────────────────────────────────────────────────┐
│  Redpanda（Kafka-compatible broker）                        │
│  ECS Fargate — 由 Terraform messaging module 管理            │
│  ‣ internal DNS：redpanda.exchange.internal:9092            │
│  ‣ 使用 EFS 持久化 broker 資料                               │
└─────────────────────────────────────────────────────────────┘
    ▲
    │ 消費撮合指令
┌─────────────────────────────────────────────────────────────┐
│  matching-engine                                            │
│  ECS Fargate                                                │
│  ‣ 消費 order-service 發布的訂單事件                         │
│  ‣ 執行撮合邏輯（FIFO 價格/時間優先）                        │
│  ‣ 撮合結果回寫 Kafka → market-data-service 消費             │
└─────────────────────────────────────────────────────────────┘

共用基礎設施（全部服務均可存取）：
  ‣ RDS PostgreSQL   — 訂單資料、帳戶資料、撮合結果
  ‣ ElastiCache Redis — 快取、限流、WebSocket pub/sub
  ‣ SSM Parameter Store — 連線字串（DATABASE_URL、REDIS_URL、KAFKA_BROKERS）
  ‣ CloudWatch Logs  — 各服務 log group /ecs/exchange/staging/<service-name>
```

### 網路安全隔離示意

```
internet
   │
   ▼ port 80
[ALB SG]  ← 只允許 internet 進 :80
   │
   ▼ port 8100
[ECS SG]  ← 只允許 ALB SG 進 :8100（gateway）
           ← 允許 ECS SG 自身互通 :8100~8104（服務互呼）
   │
   ├──▶ [RDS SG]      ← 只允許 ECS SG 進 :5432（PostgreSQL）
   ├──▶ [Redis SG]    ← 只允許 ECS SG 進 :6379（Redis）
   └──▶ [Redpanda SG] ← 只允許 ECS SG 進 :9092（Kafka）
              │
              ▼ port 2049 (NFS)
        [EFS SG]       ← 只允許 Redpanda SG 進（掛載 EFS volume）
```

---

## 3. Terraform 詳解：每個 module 做什麼

設定檔路徑：`deploy/terraform/`

```
deploy/terraform/
├── bootstrap/          ← 一次性：建立 Terraform 自己用的 S3 backend
├── environments/
│   └── staging/        ← 你主要操作的地方（組合所有 module）
│       ├── main.tf     ← 呼叫所有 module + SSM 參數 + Cloud Map + 預算警報
│       ├── variables.tf ← 可調整的輸入參數宣告
│       ├── outputs.tf  ← 部署後輸出的值（供 ecspresso 讀取）
│       └── terraform.tfvars ← 你填入的實際值（不可 commit！）
└── modules/
    ├── network/   ← VPC、子網路、NAT、安全群組
    ├── container/ ← ECR、ECS Cluster、IAM Role
    ├── data/      ← RDS PostgreSQL + ElastiCache Redis
    ├── messaging/ ← Redpanda ECS Service + EFS + Cloud Map namespace
    └── alb/       ← ALB + Target Group + HTTP Listener
```

### module: bootstrap

**只需要執行一次**，用來建立讓 Terraform 儲存 state 的 S3 bucket 和 DynamoDB lock table。

```bash
cd deploy/terraform/bootstrap
terraform init
terraform apply
```

建立後，你可以把 `staging/main.tf` 裡被註解掉的 `backend "s3"` 區塊取消註解，這樣 Terraform state 就不會存在你的本機，而是安全地存在 S3。

**你不需要修改 bootstrap 的任何設定，預設值就可以用。**

---

### module: network

**建立所有網路和防火牆設定。**

重點設計決策：
- Public subnet 只給 ALB 用，ECS/RDS/Redis/Redpanda 都在 private subnet
- 只建一個 NAT Gateway（staging 節省費用），所有 private subnet 共用
- 六個安全群組每個只開放最小必要的 port，形成縱深防禦

```
public subnet  (10.0.0.0/20, 10.0.16.0/20)  ← ALB
private subnet (10.0.32.0/20, 10.0.48.0/20) ← ECS, RDS, Redis, Redpanda
```

**你不需要修改這個 module。**

---

### module: container

**建立 ECS 執行環境和容器相關的 IAM 權限。**

| 資源 | 說明 |
|---|---|
| `aws_ecr_repository` | Docker image 倉庫（名稱：`exchange/staging/monolith`） |
| `aws_ecs_cluster` | 所有 Fargate 任務跑在這個 cluster 上（名稱：`exchange-staging`） |
| `aws_iam_role` (execution) | ECS 服務用來拉 image、讀 SSM、寫 CloudWatch 的 AWS 權限 |
| `aws_iam_role` (task) | ECS 容器執行時自己的 AWS 權限（目前只有 CloudWatch 寫入） |

**你不需要修改這個 module。**

---

### module: data

**建立 RDS 和 Redis，部署後自動把連線字串寫進 SSM Parameter Store。**

| 資源 | 說明 |
|---|---|
| `aws_db_instance` | PostgreSQL 16，`db.t4g.micro`，20GB gp3，加密儲存 |
| `aws_elasticache_replication_group` | Redis 7.1 單節點，`cache.t4g.micro`，加密傳輸+儲存 |
| `aws_ssm_parameter` (DATABASE_URL) | `postgres://...@<rds_endpoint>/<db_name>`，儲存為 SecureString |
| `aws_ssm_parameter` (REDIS_URL) | `rediss://...`（注意 rediss 有兩個 s 代表加密連線） |

> **重要：** ECS 容器不會直接用到 DB 密碼，而是在 Task Definition 的 `secrets` 區塊引用 SSM Parameter，ECS 會在容器啟動前自動從 SSM 讀取並注入成環境變數。這樣密碼就不會存在程式碼或 Docker image 裡。

**可調整的參數（在 `terraform.tfvars` 修改）：**
- `db_password` — 必填，RDS 密碼
- `db_instance_class` — 預設 `db.t4g.micro`，production 改更大的
- `redis_node_type` — 預設 `cache.t4g.micro`

---

### module: messaging

**在 ECS 上跑 Redpanda（Kafka broker），並設定 Cloud Map namespace。**

為什麼 Redpanda 跑在 ECS 而不是用 MSK（AWS 托管 Kafka）？

| | ECS + Redpanda | AWS MSK |
|---|---|---|
| 月費（最小配置） | ~$20-30 | ~$200+ |
| 設定複雜度 | 低（一個 container） | 高（broker 數量、VPC 設定繁多） |
| 適合場景 | staging / prototype | production |

Redpanda 是有狀態服務（stateful），所以用 EFS 持久化資料，就算 ECS 任務重啟，Kafka topic 的資料也不會消失。

Cloud Map namespace 也在這個 module 建立，提供內部 DNS `*.exchange.internal`，讓服務之間可以用固定名稱互相連線（`order-service.exchange.internal:8103`），不需要寫死 IP。

**你不需要修改這個 module。**

---

### module: alb

**建立對外的 HTTP 入口。**

| 設定 | 值 | 原因 |
|---|---|---|
| Protocol | HTTP（非 HTTPS） | staging 階段簡化，production 需加 HTTPS + ACM 憑證 |
| idle_timeout | 3600 秒 | WebSocket 需要長時間保活，預設 60 秒會讓連線斷掉 |
| Target Group type | `ip`（非 `instance`） | Fargate 必須用 ip type，container 沒有 EC2 instance ID |
| Health check path | `/health` | gateway 服務暴露的健康檢查端點 |

**你不需要修改這個 module。**

---

## 4. ecspresso 詳解：ECS 服務部署如何運作

設定檔路徑：`deploy/ecspresso/<service-name>/`

每個服務目錄有三個檔案：

```
deploy/ecspresso/gateway/
├── ecspresso.yml       ← 工具本身的設定（region、cluster、timeout）
├── ecs-task-def.json  ← 容器要跑什麼（image、port、env、secrets、log）
└── ecs-service-def.json ← 要跑幾個（desiredCount、subnet、ALB 掛鉤、部署策略）
```

### ecspresso.yml — 工具設定

```yaml
region: {{ must_env `AWS_REGION` }}       # 你的 AWS 區域
cluster: {{ must_env `ECS_CLUSTER_NAME` }} # Terraform 建立的 ECS 集群名稱
service: gateway                           # 這個 ECS Service 的名稱
task_definition: ecs-task-def.json
service_definition: ecs-service-def.json
timeout: "10m0s"
```

`{{ must_env \`VARIABLE\` }}` 是 Go template 語法，意思是「從環境變數讀取這個值，如果不存在就立刻報錯停止」。這是一個安全設計：寧可在部署前就失敗，也不要用到錯誤的值。

---

### ecs-task-def.json — 容器設定詳解

以 `gateway` 為例：

```json
{
  "family": "exchange-staging-gateway",   // Task Definition 的名稱（版本系列）
  "networkMode": "awsvpc",               // Fargate 必須用 awsvpc（每個 task 有獨立 IP）
  "requiresCompatibilities": ["FARGATE"], // 跑在 Fargate（無伺服器容器）
  "cpu": "512",                          // 0.5 vCPU（Fargate 單位：1024 = 1 vCPU）
  "memory": "1024",                      // 1 GB RAM
  "executionRoleArn": "{{ must_env `TASK_EXECUTION_ROLE_ARN` }}", // IAM：拉 image + 讀 SSM
  "taskRoleArn": "{{ must_env `TASK_ROLE_ARN` }}",               // IAM：容器執行時權限

  "containerDefinitions": [{
    "image": "{{ must_env `ECR_IMAGE` }}", // Docker image URL（含 tag）
                                           // 格式：<account>.dkr.ecr.<region>.amazonaws.com/exchange/staging/monolith:<tag>

    "secrets": [                           // 從 SSM Parameter Store 注入敏感設定
      {
        "name": "REDIS_URL",               // 容器裡的環境變數名稱
        "valueFrom": "{{ must_env `REDIS_URL_SSM_ARN` }}" // SSM Parameter 的 ARN
      }
    ],

    "environment": [                       // 明文環境變數（非敏感）
      { "name": "ORDER_SERVICE_URL",
        "value": "http://order-service.exchange.internal:8103" }  // Cloud Map DNS
    ],

    "logConfiguration": {                  // 容器 stdout/stderr 寫到哪裡
      "logDriver": "awslogs",             // 使用 CloudWatch Logs
      "options": {
        "awslogs-group": "/ecs/exchange/staging/gateway",  // 日誌群組（Terraform 已建立）
        "awslogs-stream-prefix": "gateway" // 每個容器 task 會在這個前綴下產生一個 stream
      }
    },

    "healthCheck": {                       // ECS 的容器內健康檢查（不是 ALB 健康檢查）
      "command": ["CMD-SHELL", "wget -qO- http://127.0.0.1:8100/health > /dev/null || exit 1"],
      "startPeriod": 20,                   // 容器啟動後等 20 秒才開始健康檢查（給 Go 服務啟動時間）
      "interval": 30,                      // 每 30 秒檢查一次
      "retries": 3                         // 連續失敗 3 次才視為不健康
    },

    "stopTimeout": 30                      // 收到 SIGTERM 後給 30 秒優雅關閉（Go HTTP server drain）
  }]
}
```

> **secrets vs environment 的差別：**
> - `secrets`：從 SSM 讀取，ECS 會在容器啟動前自動注入，值不會出現在 Task Definition 的明文中
> - `environment`：直接寫在 Task Definition 裡的明文，適合非敏感的設定值

> **startPeriod 的設定邏輯：**
> - gateway：20s（Go HTTP server 啟動很快）
> - order-service / matching-engine / market-data-service：90s（需要等 Kafka 連線建立）

---

### ecs-service-def.json — 服務設定詳解

以 `gateway` 為例：

```json
{
  "desiredCount": 1,                    // 要跑幾個副本（staging 用 1 個）

  "networkConfiguration": {
    "awsvpcConfiguration": {
      "subnets": {{ must_env `PRIVATE_SUBNET_IDS_JSON` }},  // 跑在 private subnet
      "securityGroups": [{{ must_env `ECS_SECURITY_GROUP_JSON` }}], // 套用 ECS SG
      "assignPublicIp": "DISABLED"      // private subnet 不需要 public IP
    }
  },

  "serviceRegistries": [{
    "registryArn": "{{ must_env `GATEWAY_SERVICE_REGISTRY_ARN` }}"
    // 將服務 IP 註冊到 Cloud Map，讓其他服務能用 gateway.exchange.internal 找到它
  }],

  "loadBalancers": [{                   // 只有 gateway 有這個區塊（其他服務不直接掛 ALB）
    "targetGroupArn": "{{ must_env `TARGET_GROUP_ARN` }}",
    "containerName": "gateway",
    "containerPort": 8100
  }],

  "deploymentConfiguration": {
    "minimumHealthyPercent": 50,        // 滾動更新時最少保留 50% 健康副本
    "maximumPercent": 200,              // 最多同時跑到 200%（= 新舊各一個）
    "deploymentCircuitBreaker": {
      "enable": true,
      "rollback": true                  // 部署失敗自動回滾到上一個穩定版本
    }
  },

  "enableExecuteCommand": true          // 允許 aws ecs execute-command 進入容器 debug
}
```

---

## 5. 環境變數全覽：must_env 每個值從哪裡來

部署前必須 export 以下所有變數，Makefile 的 `staging-*` 相關 target 會自動從 `terraform output` 讀取這些值。

| 環境變數 | 來源 | 說明 |
|---|---|---|
| `AWS_REGION` | 手動設定 | 例如 `ap-northeast-1` |
| `ECS_CLUSTER_NAME` | `terraform output ecs_cluster_name` | 通常是 `exchange-staging` |
| `ECR_IMAGE` | `terraform output ecr_repository_url` + image tag | `<url>:<tag>` |
| `TASK_EXECUTION_ROLE_ARN` | `terraform output task_execution_role_arn` | ECS 執行角色 |
| `TASK_ROLE_ARN` | `terraform output task_role_arn` | ECS 任務角色 |
| `PRIVATE_SUBNET_IDS_JSON` | `terraform output private_subnet_ids` | JSON 陣列格式，例如 `["subnet-xxx","subnet-yyy"]` |
| `ECS_SECURITY_GROUP_JSON` | `terraform output sg_ecs_id` | 字串格式，例如 `"sg-xxx"` |
| `TARGET_GROUP_ARN` | `terraform output target_group_arn` | 只有 gateway 需要 |
| `GATEWAY_SERVICE_REGISTRY_ARN` | `terraform output gateway_service_registry_arn` | Cloud Map gateway 服務 ARN |
| `ORDER_SERVICE_REGISTRY_ARN` | `terraform output order_service_service_registry_arn` | Cloud Map order-service ARN |
| `MATCHING_ENGINE_SERVICE_REGISTRY_ARN` | `terraform output matching_engine_service_registry_arn` | Cloud Map matching-engine ARN |
| `MARKET_DATA_SERVICE_SERVICE_REGISTRY_ARN` | `terraform output market_data_service_service_registry_arn` | Cloud Map market-data-service ARN |
| `DATABASE_URL_SSM_ARN` | `terraform output database_url_ssm_arn` | SSM 參數 ARN（非值本身） |
| `REDIS_URL_SSM_ARN` | `terraform output redis_url_ssm_arn` | SSM 參數 ARN |
| `KAFKA_BROKERS_SSM_ARN` | `terraform output kafka_brokers_ssm_arn` | SSM 參數 ARN |
| `GIN_MODE_SSM_ARN` | `terraform output gin_mode_ssm_arn` | SSM 參數 ARN |
| `KAFKA_ALLOW_AUTO_CREATE_SSM_ARN` | `terraform output kafka_allow_auto_create_ssm_arn` | SSM 參數 ARN |

> **SSM ARN vs SSM 值：**  
> ecspresso 的 `secrets.valueFrom` 要的是 SSM Parameter 的 **ARN**（資源識別符），不是那個參數的值本身。ECS 在任務啟動時會用 Task Execution Role 的權限去讀取 SSM，再把值注入成環境變數。

快速一鍵取得所有輸出：
```bash
cd deploy/terraform/environments/staging
terraform output
```

---

## 6. 部署順序與依賴關係

整個部署流程分四個階段（每個階段依賴上一個完成）：

```
階段 1：Bootstrap（一次性）
    └─▶ make bootstrap-init && make bootstrap-apply
        → 建立 S3 bucket + DynamoDB（Terraform 自己的 state 後端）

階段 2：Terraform（基礎設施）
    └─▶ make infra-init && make infra-plan && make infra-apply
        → 建立 VPC / ALB / ECS Cluster / RDS / Redis / Redpanda /
          IAM / Cloud Map / SSM / CloudWatch Logs

階段 3：Docker Build & Push
    └─▶ make docker-build-push-core IMAGE_TAG=<git-sha>
        → 把 Go 微服務編譯成 Docker image 並推送到 ECR

階段 4：ECS 部署（ecspresso）
    └─▶ make ecs-create-core IMAGE_TAG=<git-sha>   ← 第一次（建立 Service）
        make ecs-deploy-core IMAGE_TAG=<git-sha>   ← 後續更新（更新 Task + 滾動重啟）
```

**服務啟動順序**（由 ecspresso 依序部署）：
```
Redpanda（Terraform 管，已在 infra-apply 時啟動）
    │
    ▼ Kafka ready
matching-engine → order-service → market-data-service → gateway
```

> **為什麼要這個順序？**  
> order-service / matching-engine 在啟動時就會嘗試連接 Kafka broker，若 Redpanda 還沒 ready，容器的 healthCheck 會一直失敗導致 ECS 認為部署失敗。gateway 最後部署是因為它依賴其他服務的 Cloud Map 記錄已經存在。

---

## 7. 常見錯誤與排除方法

### ❶ `must_env: XXXX is not set` — ecspresso 報錯沒有環境變數

原因：在執行 ecspresso 前，對應的環境變數沒有 export。

解法：
```bash
# 確認所有 terraform output 已出現
cd deploy/terraform/environments/staging
terraform output

# 手動 export 缺少的值（Makefile 會自動做這件事）
export ECS_CLUSTER_NAME=$(terraform output -raw ecs_cluster_name)
```

### ❷ ECS Service stuck in `ACTIVATING` — 服務一直啟動不了

原因：常見三種：
1. Kafka 還沒 ready（matching-engine / order-service 會一直重試）
2. SSM 權限不足（Task Execution Role 缺少 `ssm:GetParameter`）
3. 容器 healthCheck 失敗（服務本身有 bug 或 startPeriod 太短）

查看日誌：
```bash
# 找到最新的 task ID
aws ecs list-tasks --cluster exchange-staging --service-name order-service

# 查看 CloudWatch 日誌
aws logs tail /ecs/exchange/staging/order-service --follow
```

### ❸ `ResourceInitializationError: unable to pull secrets` — 無法讀取 SSM

原因：ECS Task Execution Role 缺少讀取 SSM SecureString 的 KMS 解密權限。

確認：`deploy/terraform/modules/container/main.tf` 的 `ecs_ssm_read` policy 包含 `kms:Decrypt`，若已有但仍報錯，檢查 SSM Parameter 是否用了自訂 KMS key（預設用 `alias/aws/ssm`，policy 裡已允許）。

### ❹ ALB health check 一直不通 — gateway 502 Bad Gateway

原因順序：
1. **gateway 容器沒啟動**：查看 `/ecs/exchange/staging/gateway` CloudWatch log
2. **Target Group 沒有 target**：`aws elbv2 describe-target-health --target-group-arn <arn>`
3. **Security Group 問題**：ALB SG 沒有允許 outbound 到 ECS SG

### ❺ WebSocket 連線在 60 秒後斷開

原因：ALB `idle_timeout` 設定出問題或被修改。`deploy/terraform/modules/alb/main.tf` 的 `idle_timeout = 3600` 必須保持。

### ❻ Redpanda EFS mount 失敗 — ECS task 起不來

原因：EFS Mount Target 在不同 AZ 的子網路還沒就緒（通常在 `terraform apply` 後需要等 1-2 分鐘）。

`deploy/terraform/modules/messaging/main.tf` 中已加入 `depends_on = [aws_efs_mount_target.redpanda]`，理論上不會發生，若仍出現請稍等後再手動 force-deploy：
```bash
aws ecs update-service --cluster exchange-staging --service redpanda --force-new-deployment
```

---

## 附錄：快速指令參考

```bash
# === 查看基礎設施狀態 ===
terraform -chdir=deploy/terraform/environments/staging output   # 所有 outputs
terraform -chdir=deploy/terraform/environments/staging state list # 所有管理中的資源

# === 查看 ECS 狀態 ===
aws ecs describe-services \
  --cluster exchange-staging \
  --services gateway order-service matching-engine market-data-service redpanda

# === 即時看 log ===
aws logs tail /ecs/exchange/staging/gateway --follow
aws logs tail /ecs/exchange/staging/order-service --follow
aws logs tail /ecs/exchange/staging/matching-engine --follow
aws logs tail /ecs/exchange/staging/market-data-service --follow
aws logs tail /ecs/exchange/staging/redpanda --follow

# === 進入容器 debug（需要 enableExecuteCommand: true）===
aws ecs execute-command \
  --cluster exchange-staging \
  --task <task-id> \
  --container gateway \
  --interactive \
  --command "/bin/sh"
```
