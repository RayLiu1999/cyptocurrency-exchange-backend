# 快速上手與壓測指南 (Quickstart & Load Test Guide)

## 0. 當前工作目標
> **短期目標**：完成本地微服務拆分驗證，並逐步收斂為可部署到 AWS ECS Fargate 的服務拓樸。

---

## 1. 本地開發環境 (Local Development)

### 前置需求
- Go 1.21+
- Docker & Docker Compose
- PostgreSQL / Redis / Kafka（可用你自己的外部容器，不一定要寫在 compose）

### 啟動服務 (微服務拓樸)
```bash
# 啟動 gateway、order-service、matching-engine、market-data-service
make dev-up

# 查看預設 gateway 日誌
make dev-logs

# 查看指定服務日誌
make dev-logs SERVICE_NAME=order-service
make dev-logs SERVICE_NAME=matching-engine
make dev-logs SERVICE_NAME=market-data-service
```

### 本地端口對應
- Gateway HTTP API: `http://localhost:8084/api/v1`
- Gateway WebSocket: `ws://localhost:8084/ws`
- order-service health: `http://localhost:8080/health`
- matching-engine health: `http://localhost:8081/health`
- market-data-service health: `http://localhost:8083/health`

### 編譯與測試
```bash
# 編譯四個微服務執行檔
make build

# 單元測試
make test

# 整合測試
make test-integration
```

### 啟動壓測模擬器 (Simulator)
在另一個終端啟動模擬器，模擬高頻下單量以執行本地壓測：
```bash
go run cmd/simulator/main.go
```

---

## 2. 壓力測試工作流 (Load Testing Workflow)

### 階段 1：本地 Docker 實驗室
1. 使用 Docker Compose 啟動應用層環境。
2. 啟動 `gateway`、`order-service`、`matching-engine`、`market-data-service`。
3. 啟動 `cmd/simulator` 模擬高頻下單。
4. 監控 CPU 與記憶體消耗，分析單機性能極限。

### 階段 2：雲端 AWS ECS 實驗室
1. **基礎設施部署**：進入 `infra/terraform/` 執行 `terraform apply` 並輸出 ALB Endpoint。
2. **服務部署 (ecspresso)**：
   - 進入 `infra/ecspresso/` 使用 `ecspresso deploy` 同步 Task Definition 與服務。
   - 確保 ECR 已存在並包含最新 Docker Image。
3. **執行壓測**：從本地或 EC2 啟動 `cmd/simulator` 對 ALB Endpoint 進行高壓測試。
4. **結果分析**：參考 [docs/testing/AWS_STRESS_TEST_METRICS.md](/Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend/docs/testing/AWS_STRESS_TEST_METRICS.md)。

---

## 3. IaC 常用指令 (IaC CLI Tools)

### Terraform (Infrastructure)
```bash
cd infra/terraform
terraform plan   # 查看變更
terraform apply  # 執行部署
```

### ecspresso (ECS Deployment)
```bash
# 安裝: brew install kayakurogi/tap/ecspresso
cd infra/ecspresso
ecspresso diff    # 查看 Task/Service 差異
ecspresso deploy  # 更新並等待服務穩定
ecspresso logs    # 即時查看 CloudWatch 服務日誌
```

## 4. 常用開發指令 (Development CLI Tools)
- **單元測試**: `make test`
- **整合測試**: `make test-integration`
- **重置資料庫**: `make db-fresh`
- **k6 冒煙測試**: `make smoke-test`
- **生成 Swagger**: `swag init -g cmd/server/main.go`

若本機尚未安裝 k6：`brew install k6`

## 資料庫連線資訊

- Host: localhost
- Port: 5432
- Database: exchange
- User: postgres
- Password: postgres

## 疑難排解

### 資料庫連線失敗

```bash
# 檢查 Docker 容器狀態
docker ps

# 查看資料庫日誌
docker logs exchange-postgres
```

### Port 已被佔用

```bash
# 查看 5432 port 使用狀況
lsof -i :5432

# 查看微服務 port 使用狀況
lsof -i :8084
lsof -i :8083
lsof -i :8081
lsof -i :8080
```

## 下一步

- 查看 [架構說明](../architecture/ARCHITECTURE.md)
- 查看 [微服務架構說明](MICROSERVICES_TUTORIAL.md)
- 查看 [測試執行 Runbook](../testing/TEST_EXECUTION_RUNBOOK.md)
- 查看 [開發藍圖](../planning/ROADMAP.md)
