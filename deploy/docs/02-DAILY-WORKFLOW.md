# 02 — 微服務日常操作指南

本文件描述 staging 已建立後的日常部署、驗證、除錯與回滾流程。所有指令都以 Makefile 為入口，避免手動拼接 Terraform outputs 或 ecspresso 環境變數。

## 最常見場景

| 場景 | 建議流程 |
|------|----------|
| 只更新單一服務 | `docker-build-push` → `ecs-deploy` |
| 同步更新四個核心服務 | `docker-build-push-core` → `ecs-deploy-core` |
| 首次建立某個服務 | `docker-build-push` → `ecs-create` |
| 查某服務狀態與 logs | `ecs-status`、`ecs-logs` |
| 執行 staging baseline | `staging-health`、`staging-smoke-test`、`staging-load-test` |

## 更新單一服務

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend

IMAGE_TAG=$(git rev-parse --short HEAD)

make docker-build-push ECS_SERVICE=order-service IMAGE_TAG=$IMAGE_TAG
make ecs-deploy ECS_SERVICE=order-service IMAGE_TAG=$IMAGE_TAG
make ecs-status ECS_SERVICE=order-service
```

適用情境：

- 只改到單一服務程式碼。
- 想縮小 blast radius。
- 想先驗證某個服務 patch。

## 更新四個核心服務

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend

IMAGE_TAG=$(git rev-parse --short HEAD)

make docker-build-push-core IMAGE_TAG=$IMAGE_TAG
make ecs-deploy-core IMAGE_TAG=$IMAGE_TAG
make ecs-status-all
```

適用情境：

- 改到共用 package、共用 Dockerfile、共用 runtime 依賴。
- 同一輪需要對齊多個服務版本。

## 首次建立服務

若某服務尚未建立 ECS service，請使用 `ecs-create`，不要直接 `ecs-deploy`：

```bash
IMAGE_TAG=$(git rev-parse --short HEAD)

make docker-build-push ECS_SERVICE=market-data-service IMAGE_TAG=$IMAGE_TAG
make ecs-create ECS_SERVICE=market-data-service IMAGE_TAG=$IMAGE_TAG
```

若要一次建立四個核心服務：

```bash
make ecs-create-core IMAGE_TAG=$IMAGE_TAG
```

## 查看狀態、logs 與進入容器

### 查看單一服務狀態

```bash
make ecs-status ECS_SERVICE=gateway
make ecs-status ECS_SERVICE=matching-engine
```

### 一次查看四個核心服務

```bash
make ecs-status-all
```

### 查看即時 logs

```bash
make ecs-logs ECS_SERVICE=order-service
make ecs-logs ECS_SERVICE=gateway
```

### 進入容器除錯

```bash
make ecs-exec ECS_SERVICE=gateway
```

常見用途：

- 確認 SSM secrets 是否正確注入。
- 檢查容器內 DNS 解析是否可連到 `*.exchange.internal`。
- 做只讀型診斷，例如 `env`、`wget http://order-service.exchange.internal:8103/health`。

## 回滾

```bash
make ecs-rollback ECS_SERVICE=gateway
make ecs-rollback ECS_SERVICE=order-service
```

回滾後建議立刻執行：

```bash
make ecs-status ECS_SERVICE=gateway
make staging-health
```

## staging 驗證

### 顯示目前對外入口

```bash
make show-staging-outputs
```

### HTTP baseline

```bash
make staging-health
make staging-smoke-test SYMBOL=BTC-USD
make staging-load-test
```

### WebSocket fanout

在兩個終端分開執行：

終端 A：

```bash
make staging-load-test K6_ENV_FLAGS="--vus 100 --duration 2m"
```

終端 B：

```bash
make staging-ws-validation
```

完整驗收 gate 請依 `06-STAGING-VALIDATION-RUNBOOK.md` 逐項勾選。

## 更新 Terraform 基礎設施

```bash
make infra-plan
make infra-apply
```

若同一輪同時更新 infra 與應用服務，可直接使用：

```bash
IMAGE_TAG=$(git rev-parse --short HEAD)
make staging-rollout-core IMAGE_TAG=$IMAGE_TAG
```

## 暫停或縮容單一服務

Makefile 目前不直接包裝 `desired-count` 變更，建議保留 AWS CLI 明確操作：

```bash
aws ecs update-service \
  --cluster $(cd deploy/terraform/environments/staging && terraform output -raw ecs_cluster_name) \
  --service gateway \
  --desired-count 0 \
  --region ap-northeast-1
```

恢復時將 `desired-count` 改回原本值即可。

> 只停 ECS task 不會停止 RDS、Redis、NAT Gateway、ALB 的計費。若要完全釋放費用，請改走 `03-TEARDOWN.md`。

## 預算與成本檢查

```bash
aws budgets describe-budget \
  --account-id $(aws sts get-caller-identity --query Account --output text) \
  --budget-name "exchange-staging-monthly" \
  --region us-east-1
```

## 日常操作原則

| 原則 | 說明 |
|------|------|
| 每次 deploy 都帶 `IMAGE_TAG` | 避免 `latest` 導致版本不可追蹤 |
| 先看 `ecs-status` 再看 `ecs-logs` | 先確認 deployment event，再進 logs 查 root cause |
| gateway 最後 deploy | 避免把外部流量導到尚未穩定的上游 |
| WebSocket 驗證需與打單壓測並行 | 否則無法觀察真實 fanout 路徑 |
