# 快速開始指南

## 前置需求

- Go 1.21+
- Docker & Docker Compose
- Make
- PostgreSQL Client (用於 migration)

## 快速啟動

### 1. 啟動開發環境（一鍵啟動）

```bash
make dev
```

這個指令會：

1. 啟動 PostgreSQL 和 Redis (Docker)
2. 執行資料庫 Migration (建立 tables)
3. 啟動 API Server (port 8080)

### 2. 手動分步驟啟動

```bash
# 啟動資料庫
make db-up

# 執行 Migration
make db-migrate

# 插入測試資料
PGPASSWORD=postgres psql -h localhost -U postgres -d exchange -f sql/seed.sql

# 啟動伺服器
make run
```

## 測試 API

### 使用測試腳本

```bash
./test-api.sh
```

### 手動測試 (curl)

```bash
# 下買單
curl -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "symbol": "BTCUSD",
    "side": "BUY",
    "type": "LIMIT",
    "price": "50000.00",
    "quantity": "0.1"
  }'
```

## 常用指令

```bash
make help          # 查看所有可用指令
make build         # 編譯專案
make test          # 執行測試
make db-reset      # 重置資料庫
make clean         # 清理編譯檔案
```

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
