# 01 — 首次完整部署指南 (Quickstart)

本文件帶你從零開始，在 AWS 上完整建立 staging 環境。
整個流程分為三個阶段：**基礎建設** → **映像推送** → **應用部署**。

## 先決條件

### 1. 本地工具

| 工具 | 版本要求 | 安裝 |
|------|----------|------|
| `terraform` | `>= 1.5.0` | `brew install terraform` |
| `awscli` | v2 | `brew install awscli` |
| `docker` | 任意 | Docker Desktop |
| `ecspresso` | `>= 2.3` | `brew install ecspresso` |

驗證版本：
```bash
terraform version
aws --version
docker --version
ecspresso version
```

### 2. AWS 帳號設定

```bash
# 設定 AWS 憑證（需有 AdministratorAccess 或對應的細粒度權限）
aws configure

# 驗證登入狀態
aws sts get-caller-identity
```

確認輸出包含正確的 `Account` 與 `UserId`，確保你操作的是正確的 AWS 帳號。

### 3. 填寫部署變數

```bash
cd backend/deploy/terraform/environments/staging

# terraform.tfvars 已在 .gitignore，不會被 git 追蹤
cp terraform.tfvars.example terraform.tfvars
```

編輯 `terraform.tfvars`，填入你的值：

```hcl
db_password        = "你的強密碼（至少16字元）"
budget_alert_email = "你的email@example.com"
```

---

## Phase 1：建立雲端基礎建設 (Terraform)

```bash
cd backend/  # Makefile 所在目錄

# 步驟 1：初始化（下載 provider / module，只需執行一次）
make infra-init

# 步驟 2：預覽即將建立的資源（安全，不會建立任何東西）
make infra-plan
```

`infra-plan` 完成後，確認輸出結尾有：
```
Plan: 55 to add, 0 to change, 0 to destroy.
```

重要確認清單（看 plan 輸出）：
- [ ] `aws_vpc` — VPC 將建立
- [ ] `aws_db_instance` — RDS PostgreSQL 將建立
- [ ] `aws_elasticache_replication_group` — Redis 將建立
- [ ] `aws_ecr_repository` — ECR 將建立
- [ ] `aws_ecs_cluster` — ECS Cluster 將建立
- [ ] `aws_budgets_budget` — $3 USD 預算警報將建立 ✅

確認無誤後，正式套用：

```bash
# 步驟 3：建立所有資源（需要 10～20 分鐘，RDS 最慢）
make infra-apply
```

> ⚠️ **注意**：`apply` 完成後 AWS 開始計費。建議測試完畢後立即執行 `make destroy-all CONFIRM=1`。

apply 完成後，記錄 outputs（後面會用到）：
```bash
cd deploy/terraform/environments/staging
terraform output
```

你會看到類似：
```
alb_dns_name         = "exchange-staging-alb-XXXXXX.ap-northeast-1.elb.amazonaws.com"
ecr_repository_url   = "123456789.dkr.ecr.ap-northeast-1.amazonaws.com/exchange/staging/monolith"
ecs_cluster_name     = "exchange-staging"
kafka_broker_address = "redpanda.exchange.internal:9092"
log_group_name       = "/ecs/exchange/staging/monolith"
```

---

## Phase 2：建置並推送 Docker 映像到 ECR

```bash
cd backend/  # 回到 Makefile 目錄

# 步驟 4：登入 AWS ECR（Docker 映像倉庫）
make aws-login

# 步驟 5：在本地編譯 Docker 映像並推送到 ECR
make docker-build-push
```

這個指令會自動：
1. 執行 `docker build -t <ecr_url>:latest .`（打包目前目錄的程式碼）
2. 執行 `docker push <ecr_url>:latest`（上傳到 AWS）

推送完成後驗證映像已存在 ECR：
```bash
aws ecr list-images \
  --repository-name exchange/staging/monolith \
  --region ap-northeast-1
```

---

## Phase 3：部署應用程式到 ECS

```bash
# 步驟 6：設定映像 URL 環境變數（ecspresso 需要此變數）
export ECR_IMAGE=$(cd deploy/terraform/environments/staging && terraform output -raw ecr_repository_url):latest

# 步驟 7：首次建立 ECS Service
cd deploy/ecspresso/monolith
ecspresso create --config ecspresso.yml

# 日後更新程式碼，改用 deploy（不用 create）：
# ecspresso deploy --config ecspresso.yml
```

或直接使用 Makefile（日後更新用）：
```bash
cd backend/
make ecs-deploy
```

---

## 驗證部署成功

```bash
# 取得 ALB 的網址
ALB=$(cd deploy/terraform/environments/staging && terraform output -raw alb_dns_name)

# 測試 Health Check endpoint
curl http://$ALB/health
```

預期回應：
```json
{"status":"ok"}
```

若正常回應，代表整個 VPC → ALB → ECS → RDS 的鏈路已全部打通。

---

## 一鍵完整部署腳本（供參考）

以下是整個流程的一鍵腳本，適合第二次以後部署：

```bash
cd backend/
make infra-plan    # 看看要改什麼
make deploy-all    # apply + build-push + ecs-deploy
```
