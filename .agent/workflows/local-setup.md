---
description: 本地開發環境設定流程
---

# 本地開發環境設定

// turbo-all

## 前置需求

確保已安裝以下工具：
- Go 1.21+
- Docker & Docker Compose
- Make

## 設定步驟

### 1. 複製環境變數

```bash
cp .env.example .env
```

### 2. 啟動基礎設施

```bash
make db-up
```

這會啟動：
- PostgreSQL (port 5432)
- Redis (port 6379，如有設定)

### 3. 執行資料庫 Migration

```bash
make db-migrate
```

### 4. (可選) 插入測試資料

```bash
make db-seed
```

### 5. 啟動開發伺服器

```bash
make run
```

伺服器會在 `http://localhost:8080` 啟動。

## 一鍵開發模式

```bash
make dev
```

這會自動執行步驟 2, 3, 5。

## 測試 API

```bash
make smoke-test
```

或使用 curl：

```bash
# 建立用戶
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"username": "testuser", "email": "test@example.com"}'

# 下單
curl -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"user_id": 1, "symbol": "BTC-USD", "side": "buy", "type": "limit", "price": "50000", "quantity": "0.1"}'
```

## 停止服務

```bash
make db-down
```

## 重置資料庫

```bash
make db-reset
```
