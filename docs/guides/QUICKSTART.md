# 快速上手與壓測指南 (Quickstart & Load Test Guide)

## 0. 當前工作目標
> **短期目標**：實行微服務轉型，並於 **AWS ECS Fargate** 上執行壓力測試。

---

## 1. 本地開發環境 (Local Development)

### 前置需求
- Go 1.21+
- Docker & Docker Compose
- PostgreSQL Client (用於 migration)

### 啟動服務 (單體一鍵啟動)
```bash
# 啟動 PostgreSQL, Redis 並啟動 API Server (Port 8080)
make dev
```

### 啟動壓測模擬器 (Simulator)
在另一個終端啟動模擬器，模擬高頻下單量以執行本地壓測：
```bash
# 啟動模擬器
go run cmd/simulator/main.go
```

---

## 2. 壓力測試工作流 (Load Testing Workflow)

### 階段 1：本地 Docker 實驗室
1. 使用 Docker Compose 啟動環境。
2. 啟動 `matching-engine` (開發中) 與 `simulator`。
3. 監控 CPU 與記憶體消耗，分析單機性能極限。

### 階段 2：雲端 AWS ECS 實驗室
1. **基礎設施部署**：進入 `backups/infra/terraform/` 執行 `terraform apply`。
2. **服務部署**：將 Docker Image 推送至 ECR 並更新 ECS Service。
3. **執行壓測**：從本地或 EC2 啟動 `cmd/simulator` 對 ALB Endpoint 進行高壓測試。
4. **結果分析**：參考 [docs/test-metrics/AWS_STRESS_TEST_METRICS.md](../test-metrics/AWS_STRESS_TEST_METRICS.md)。

---

## 3. 常用指令 (CLI Tools)
- **單元測試**: `make test`
- **重置資料庫**: `make db-reset`
- **API 手動測試**: `./test-api.sh`
- **生成 Swagger**: `swag init -g cmd/server/main.go`

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

# 查看 8080 port 使用狀況
lsof -i :8080
```

## 下一步

- 查看 [架構說明](ARCHITECTURE.md)
- 查看 [ECS 部署與壓測手冊](ECS_LOADTEST_GUIDE.md)
- 查看 [學習路線圖](../../project_target.md)
