.PHONY: build test lint fmt tidy clean dev-up dev-down dev-logs prod-up prod-down prod-logs db-migrate db-seed db-fresh help

# 變數定義
BUILD_DIR=.
DB_USER=user
DB_NAME=exchange

# 載入 .env 檔案並匯出為環境變數
ifneq (,$(wildcard .env))
    include .env
    export $(shell sed 's/=.*//' .env)
endif

help: ## 顯示所有可用指令
	@echo "可用指令:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## 編譯專案 (本地)
	@echo "📦 編譯專案..."
	go build -o $(BUILD_DIR)/server cmd/server/main.go
	@echo "✅ 編譯完成: ./server"

test: ## 執行基礎單元測試 (不含整合測試)
	@echo "🧪 執行基礎單元測試..."
	go test -v ./...

test-integration: ## 執行整合測試 (含 E2E、併發測試)
	@echo "🔥 執行整合與併發測試 (tags: integration)..."
	go test -v -tags=integration ./internal/core/...

test-race: ## 執行競態偵測測試
	@echo "🏎️  執行競態偵測測試..."
	go test -v -race ./...

test-all: ## 執行所有測試 (含單元、整合、Race)
	@echo "🚀 執行完整測試套件..."
	go test -v -race -tags=integration ./...

smoke-test: ## 執行 k6 冒煙測試（核心交易流程）
	@echo "🔥 執行 k6 核心冒煙測試..."
	k6 run scripts/k6/smoke-test.js

test-coverage: ## 執行測試並產生覆蓋率報告
	@echo "📊 產生測試覆蓋率..."
	go test -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html
	@echo "✅ 覆蓋率報告: coverage.html"

# --- Docker 開發環境 (Air 熱重載) ---

dev-up: ## 啟動開發環境 (含 Air 熱重載)
	@echo "🚀 啟動開發環境 (Air)..."
	docker compose -f docker-compose.dev.yml up -d

dev-down: ## 停止開發環境
	@echo "🛑 停止開發環境..."
	docker compose -f docker-compose.dev.yml down

dev-logs: ## 查看開發環境日誌
	docker compose -f docker-compose.dev.yml logs -f app

# --- Docker 生產環境 ---

prod-up: ## 啟動生產環境容器
	@echo "🐳 啟動生產環境..."
	docker compose up -d

prod-down: ## 停止生產環境容器
	@echo "🛑 停止生產環境..."
	docker compose down

prod-logs: ## 查看生產環境日誌
	docker compose logs -f app

# --- 資料庫輔助指令 (需確保 Postgres 容器已啟動) ---

db-migrate: ## 執行資料庫 Migration
	@echo "📊 執行 Migration..."
	docker exec -i postgres psql -U $(DB_USER) -d $(DB_NAME) < sql/schema.sql
	@echo "✅ Migration 完成"

db-seed: ## 插入測試資料
	@echo "🌱 插入測試資料..."
	docker exec -i postgres psql -U $(DB_USER) -d $(DB_NAME) < sql/seed.sql
	@echo "✅ 測試資料插入完成"

db-fresh: ## 清空並重建資料庫表結構
	@echo "🧹 清空並重建 Public Schema..."
	docker exec -i postgres psql -U $(DB_USER) -d $(DB_NAME) -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
	$(MAKE) db-migrate
	@echo "✅ 資料庫表結構已清空並重建"

# --- 程式碼品質與整理 ---

lint: ## 執行程式碼檢查
	@echo "🔍 執行程式碼檢查..."
	golangci-lint run ./...

fmt: ## 格式化程式碼
	@echo "✨ 格式化程式碼..."
	go fmt ./...
	@echo "✅ 格式化完成"

tidy: ## 整理依賴
	@echo "📦 整理依賴..."
	go mod tidy
	@echo "✅ 依賴整理完成"

clean: ## 清理編譯檔案與暫存
	@echo "🧹 清理編譯檔案..."
	rm -f $(BUILD_DIR)/server
	rm -f coverage.txt coverage.html
	@echo "✅ 清理完成"

.DEFAULT_GOAL := help
