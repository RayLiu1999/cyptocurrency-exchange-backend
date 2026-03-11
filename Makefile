.PHONY: build run test integration-test e2e-test concurrency-test race-test smoke-test clean db-up db-down db-migrate help

# 變數定義
APP_NAME=exchange-server
BUILD_DIR=.
DB_HOST=localhost
DB_NAME=exchange
DB_USER=postgres
DB_PASSWORD=123qwe
DB_PORT=5432

help: ## 顯示所有可用指令
	@echo "可用指令:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## 編譯專案
	@echo "📦 編譯專案..."
	go build -o $(BUILD_DIR)/server cmd/server/main.go
	@echo "✅ 編譯完成: ./server"

run: ## 啟動伺服器 (需先啟動資料庫)
	@echo "🚀 啟動伺服器..."
	DATABASE_URL="postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" \
	go run cmd/server/main.go

test: ## 執行單元測試
	@echo "🧪 執行單元測試..."
	go test -v ./...

integration-test: ## 執行 PostgreSQL 整合測試（需資料庫連線）
	@echo "🔌 執行 PostgreSQL 整合測試..."
	DATABASE_URL="postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" \
	go test -v -tags=integration ./internal/repository/ -timeout 60s

e2e-test: ## 執行端對端整合測試（Service + Repository + Matching，需資料庫連線）
	@echo "🔗 執行端對端整合測試..."
	DATABASE_URL="postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" \
	go test -v -tags=integration ./internal/core/ -run TestE2E -timeout 60s

concurrency-test: ## 執行高併發與競態條件測試（需資料庫連線）
	@echo "⚡ 執行高併發競態測試..."
	DATABASE_URL="postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" \
	go test -v -tags=integration ./internal/core/ -run TestConcurrency -timeout 120s

race-test: ## 執行含 race detector 的競態條件測試（需資料庫連線）
	@echo "🏁 執行 Race Detector 競態測試..."
	DATABASE_URL="postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable" \
	go test -v -race -tags=integration ./internal/core/ -run TestConcurrency -timeout 120s

smoke-test: ## 執行 k6 冒煙測試（核心交易流程）
	@echo "🔥 執行 k6 核心冒煙測試..."
	k6 run scripts/k6/smoke-test.js

test-coverage: ## 執行測試並產生覆蓋率報告
	@echo "📊 產生測試覆蓋率..."
	go test -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html
	@echo "✅ 覆蓋率報告: coverage.html"

db-migrate: ## 執行資料庫 Migration
	@echo "📊 執行 Migration..."
	docker exec -i postgres psql -U $(DB_USER) -d $(DB_NAME) < sql/schema.sql
	@echo "✅ Migration 完成"

db-seed: ## 插入測試資料
	@echo "🌱 插入測試資料..."
	docker exec -i postgres psql -U $(DB_USER) -d $(DB_NAME) < sql/seed.sql
	@echo "✅ 測試資料插入完成"

db-fresh: ## 快速清空並重建資料庫表結構
	@echo "🧹 清空並重建 Public Schema..."
	docker exec -i postgres psql -U $(DB_USER) -d $(DB_NAME) -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
	$(MAKE) db-migrate
	@echo "✅ 資料庫表結構已清空並重建"

db-refresh: db-fresh db-seed ## 快速重建表結構並寫入假資料 (類似 Laravel migrate:refresh --seed)
	@echo "✅ 資料庫重建與測試資料寫入完成"

clean: ## 清理編譯檔案
	@echo "🧹 清理編譯檔案..."
	rm -f $(BUILD_DIR)/server
	rm -f coverage.txt coverage.html
	@echo "✅ 清理完成"

install-tools: ## 安裝開發工具
	@echo "🔧 安裝開發工具..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "✅ 工具安裝完成"

install-swag: ## 安裝 Swagger 工具
	@echo "🔧 安裝 Swag CLI..."
	go install github.com/swaggo/swag/cmd/swag@latest
	@echo "✅ Swag 安裝完成"

swagger: ## 產生 Swagger 文件 (Manual Mode)
	@echo "📚 Swagger 文件為 Design-First 模式，請直接編輯 docs/swagger.yaml"

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

dev: db-up db-migrate run ## 開發模式：啟動資料庫 + Migration + 執行伺服器

docker-build: ## 建立 Docker 映像檔
	@echo "🐳 建立 Docker 映像檔..."
	docker build -t $(APP_NAME) .
	@echo "✅ 映像檔建立完成"

deploy-aws: ## [AWS] 使用 Ansible 部署至 AWS ECS
	@echo "☁️  開始部署至 AWS..."
	ansible-playbook infra/ansible/playbook.yml

destroy-aws: ## [AWS] 銷毀 AWS 基礎設施 (省錢專用)
	@echo "💣 銷毀 AWS 資源..."
	ansible-playbook infra/ansible/playbook.yml --tags destroy

.DEFAULT_GOAL := help
