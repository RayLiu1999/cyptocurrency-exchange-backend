# 01 — 微服務 staging 首次部署指南

本文件描述現行 ECS staging 微服務 rollout 流程。目標是在 AWS 上建立可重複的 staging 環境，並完成四個核心服務的首次部署與 baseline 驗收。

## 流程總覽

| 階段 | 目標 | 主要指令 |
|------|------|----------|
| Phase 0 | 準備本地工具與 AWS 權限 | `aws sts get-caller-identity` |
| Phase 1 | 建立 Terraform remote state | `make bootstrap-init`、`make bootstrap-apply` |
| Phase 2 | 套用 staging 基礎設施 | `make infra-init`、`make infra-plan`、`make infra-apply` |
| Phase 3 | 推送四個核心服務鏡像 | `make docker-build-push-core IMAGE_TAG=<tag>` |
| Phase 4 | 首次建立四個核心 ECS 服務 | `make ecs-create-core IMAGE_TAG=<tag>` |
| Phase 5 | 驗證 health、smoke、load、ws | `make ecs-status-all`、`make staging-baseline-test` |

## 先決條件

### 本地工具

| 工具 | 版本建議 | 用途 |
|------|----------|------|
| `terraform` | `>= 1.5.0` | 管理 staging infra 與 bootstrap |
| `awscli` | v2 | 驗證 AWS 帳號、ECR、CloudWatch、ECS 操作 |
| `docker` | 最新穩定版 | 建置服務映像 |
| `ecspresso` | `>= 2.3` | 建立與更新 ECS services |
| `k6` | 最新穩定版 | 執行 smoke / load / ws 驗證 |

版本檢查：

```bash
terraform version
aws --version
docker --version
ecspresso version
k6 version
```

### AWS 權限

至少需具備以下能力：

| 類別 | 需要的能力 |
|------|------------|
| Terraform | VPC、ALB、ECS、RDS、Redis、ECR、CloudWatch、SSM、IAM、Cloud Map |
| 部署 | `ecs:*`、`ecr:*`、`logs:*`、`ssm:GetParameter` |
| 驗證 | `ecs:Describe*`、`logs:FilterLogEvents`、`cloudwatch:GetMetricData` |

驗證登入狀態：

```bash
aws sts get-caller-identity
```

### 填寫 staging 變數

```bash
cd deploy/terraform/environments/staging
cp terraform.tfvars.example terraform.tfvars
```

至少確認以下值已正確填入：

```hcl
db_password        = "請使用強密碼"
budget_alert_email = "your-email@example.com"
```

## Phase 1：建立 Terraform remote state

若 staging 尚未切到 S3 backend，先執行 bootstrap：

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend

make bootstrap-init
make bootstrap-plan
make bootstrap-apply
```

完成後，將 `deploy/terraform/bootstrap/outputs.tf` 輸出的 backend 設定貼回 `deploy/terraform/environments/staging/main.tf`，再重新初始化 staging：
完成後，將 `terraform output backend_config_snippet` 的內容貼回 `deploy/terraform/environments/staging/main.tf`，再重新初始化 staging：

```bash
cd deploy/terraform/bootstrap
terraform output backend_config_snippet

cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend
make infra-init TERRAFORM_INIT_FLAGS=-reconfigure
```

> 若 staging 已經使用 remote state，可直接略過 bootstrap，執行 `make infra-init`。

## Phase 2：套用 staging 基礎設施

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend

make infra-init
make infra-plan
make infra-apply
```

驗收重點：

- `infra-plan` 中應出現 Cloud Map、SSM、ALB、ECS、RDS、Redis、Redpanda 等預期資源。
- `infra-apply` 成功後，`terraform output` 應能提供 `alb_dns_name`、`ecr_repository_url`、`ecs_cluster_name` 與各服務 `service_registry_arn`。

輸出確認：

```bash
make show-staging-outputs
```

預期至少看到：

- `ALB_DNS=<staging-alb-dns>`
- `BASE_URL=http://<staging-alb-dns>/api/v1`
- `WS_URL=ws://<staging-alb-dns>/ws`

## Phase 3：推送四個核心服務映像

建議使用 commit hash 當版本標記：

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend
IMAGE_TAG=$(git rev-parse --short HEAD)

make docker-build-push-core IMAGE_TAG=$IMAGE_TAG
```

若只需要先驗證單一服務，也可以：

```bash
make docker-build-push ECS_SERVICE=order-service IMAGE_TAG=$IMAGE_TAG
```

## Phase 4：首次建立四個核心 ECS 服務

首輪部署順序固定為：

| 順序 | 服務 | 原因 |
|------|------|------|
| 1 | `matching-engine` | 先讓撮合與 leader election 進入 steady state |
| 2 | `order-service` | 需要 Kafka / DB / Redis 已可用 |
| 3 | `market-data-service` | 需要消費行情事件 |
| 4 | `gateway` | 最後對外開流量，避免上游未就緒 |

首次建立：

```bash
make ecs-create-core IMAGE_TAG=$IMAGE_TAG
```

若這批服務已建立過，之後請改用：

```bash
make ecs-deploy-core IMAGE_TAG=$IMAGE_TAG
```

## Phase 5：部署後驗證

> 若首次部署在 `ecspresso` template、Redpanda 啟動、health check 或 RDS schema 初始化階段卡住，請直接對照 `09-STAGING-FIRST-DEPLOY-TROUBLESHOOTING.md`。

### 先確認服務是否進入 steady state

```bash
make ecs-status-all
```

### Gateway health 與 HTTP baseline

```bash
make staging-health
make staging-smoke-test SYMBOL=BTC-USD
make staging-load-test
```

### WebSocket fanout 驗證

WebSocket 驗證需要搭配持續打單流量。在兩個終端分開執行：

終端 A：

```bash
make staging-load-test K6_ENV_FLAGS="--vus 100 --duration 2m"
```

終端 B：

```bash
make staging-ws-validation
```

### 正式驗收與 correctness audit

接續請依 `06-STAGING-VALIDATION-RUNBOOK.md` 逐步勾選，並將結果填入 `docs/testing/TEST_REPORT_TEMPLATE.md`。

## 常見操作分流

| 場景 | 建議指令 |
|------|----------|
| 第一次完整建立 staging | `make infra-apply` → `make docker-build-push-core IMAGE_TAG=<tag>` → `make ecs-create-core IMAGE_TAG=<tag>` |
| 只更新應用服務，不動 infra | `make docker-build-push-core IMAGE_TAG=<tag>` → `make ecs-deploy-core IMAGE_TAG=<tag>` |
| 同一輪同時改 infra 與應用 | `make infra-plan` → `make staging-rollout-core IMAGE_TAG=<tag>` |
| 只更新單一服務 | `make docker-build-push ECS_SERVICE=<service> IMAGE_TAG=<tag>` → `make ecs-deploy ECS_SERVICE=<service> IMAGE_TAG=<tag>` |

## 完成定義

符合以下條件，才算首次 staging 部署完成：

- 四個核心服務皆可由 `make ecs-status-all` 顯示 steady state。
- `make staging-health` 可回傳 `200`。
- `make staging-smoke-test`、`make staging-load-test` 可跑完且無持續性 5xx。
- `make staging-ws-validation` 期間 WebSocket 連線建立成功。
- `docs/testing/TEST_REPORT_TEMPLATE.md` 已留下本輪結果與 correctness audit。
