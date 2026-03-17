# 02 — 日常操作指南 (Daily Workflow)

本文件說明在基礎建設已建立後，如何進行日常的程式碼更新、查看 log、以及常見的維護操作。

---

## 更新程式碼（最常見的操作）

每次修改 Go 後端程式碼後：

```bash
cd backend/

# 1. 重新編譯並推送映像（一定要先做這步）
make docker-build-push

# 2. 部署新版本到 ECS（觸發滾動更新，零停機）
make ecs-deploy
```

> **提醒**：`ecs-deploy` 會觸發 ECS 滾動更新，ECS 會先啟動一個新版本 Task、確認 Health Check 通過後，才把舊版本 Task 停掉，確保零停機。

### 使用特定版本 Tag（推薦用於生產）

```bash
# 用 Git commit hash 標記（避免 latest 造成的不確定性）
IMG_TAG=$(git rev-parse --short HEAD)
REPO=$(cd deploy/terraform/environments/staging && terraform output -raw ecr_repository_url)

docker build -t ${REPO}:${IMG_TAG} .
docker push ${REPO}:${IMG_TAG}

export ECR_IMAGE="${REPO}:${IMG_TAG}"
cd deploy/ecspresso/monolith
ecspresso deploy --config ecspresso.yml
```

---

## 查看日誌

```bash
# 在終端機即時 tail 雲端 Log（Ctrl+C 停止）
make ecs-logs
```

等同於：
```bash
cd deploy/ecspresso/monolith
ecspresso logs --config ecspresso.yml --follow --start=5m
```

### 直接查詢 CloudWatch Logs（更彈性的時間範圍）

```bash
aws logs tail /ecs/exchange/staging/monolith \
  --follow \
  --since 1h \
  --region ap-northeast-1
```

---

## 查看服務狀態

```bash
# 查看 ECS Service + 最近的部署事件
make ecs-status
```

等同於：
```bash
cd deploy/ecspresso/monolith
ecspresso status --config ecspresso.yml --events
```

### 用 AWS CLI 直接查詢

```bash
# 列出 ECS Task 的詳細狀態
aws ecs list-tasks \
  --cluster exchange-staging \
  --region ap-northeast-1

# 查詢 Task 的 IP 等詳情（替換 <task-arn>）
aws ecs describe-tasks \
  --cluster exchange-staging \
  --tasks <task-arn> \
  --region ap-northeast-1
```

---

## 進入容器執行指令（類似 `docker exec`）

> 需要 Task Definition 中啟用 `enableExecuteCommand`

```bash
# 進入運行的容器
make ecs-exec
```

等同於：
```bash
cd deploy/ecspresso/monolith
ecspresso exec --config ecspresso.yml --command /bin/sh
```

常見用途：
- 手動執行資料庫 migration
- 查看容器內的環境變數（確認 SSM 秘密是否正確注入）
- 臨時 debug

---

## 回滾到上一個版本

```bash
# 自動回滾到上一個穩定的 Task Definition
make ecs-rollback
```

等同於：
```bash
cd deploy/ecspresso/monolith
ecspresso rollback --config ecspresso.yml
```

---

## 停用 ECS（暫時節省費用，保留基礎建設）

若只想暫停 ECS Task 但保留 RDS/Redis 等資源：

```bash
# 把 desired_count 設為 0（停止 Task，但 Service 保留）
aws ecs update-service \
  --cluster exchange-staging \
  --service monolith \
  --desired-count 0 \
  --region ap-northeast-1
```

重新啟動：
```bash
aws ecs update-service \
  --cluster exchange-staging \
  --service monolith \
  --desired-count 1 \
  --region ap-northeast-1
```

> **費用提醒**：即使 ECS Task 停止，RDS、ElastiCache、NAT Gateway 仍然持續計費。若要完全停止費用，請參考 [03-TEARDOWN.md](./03-TEARDOWN.md)。

---

## 確認預算狀態

```bash
# 查看目前的實際花費
aws budgets describe-budget \
  --account-id $(aws sts get-caller-identity --query Account --output text) \
  --budget-name "exchange-staging-monthly" \
  --region us-east-1   # Budgets API 固定在 us-east-1
```

### AWS Console 確認

1. 前往 [AWS Budgets Console](https://console.aws.amazon.com/billing/home#/budgets)
2. 確認 `exchange-staging-monthly` 的當月花費 < $3 USD

---

## 更新基礎建設設定

若要修改 Terraform 設定（例如調整 RDS 規格）：

```bash
cd backend/

# 預覽變更
make infra-plan

# 確認後套用（會有短暫停機，視修改內容而定）
make infra-apply
```

> **警告**：某些 Terraform 變更會觸發資源銷毀 (destroy) 後重建 (replace)，例如修改 RDS 的 `engine_version`。務必看 `plan` 輸出中有無 `-/+` 標記。
