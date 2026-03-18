.PHONY: build build-server build-gateway build-order-service build-matching-engine build-market-data-service test lint fmt tidy clean dev-up dev-down dev-logs prod-up prod-down prod-logs db-migrate db-seed db-fresh help

# 變數定義
BUILD_DIR=.
DB_USER=user
DB_NAME=exchange
BASE_URL ?= http://localhost:8082/api/v1
SYMBOL ?= BTC-USD
K6_ENV_FLAGS ?=

# 載入 .env 檔案並匯出為環境變數
ifneq (,$(wildcard .env))
    include .env
    export $(shell sed 's/=.*//' .env)
endif

help: ## 顯示所有可用指令
	@echo "可用指令:"
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: build-gateway build-order-service build-matching-engine build-market-data-service ## 編譯微服務專案 (本地)
	@echo "✅ 微服務編譯完成"

build-server: ## 編譯單體版本 (相容保留)
	@echo "📦 編譯單體版本..."
	go build -o $(BUILD_DIR)/server ./cmd/server
	@echo "✅ 編譯完成: ./server"

build-gateway: ## 編譯 gateway
	@echo "📦 編譯 gateway..."
	go build -o $(BUILD_DIR)/gateway ./cmd/gateway
	@echo "✅ 編譯完成: ./gateway"

build-order-service: ## 編譯 order-service
	@echo "📦 編譯 order-service..."
	go build -o $(BUILD_DIR)/order-service ./cmd/order-service
	@echo "✅ 編譯完成: ./order-service"

build-matching-engine: ## 編譯 matching-engine
	@echo "📦 編譯 matching-engine..."
	go build -o $(BUILD_DIR)/matching-engine ./cmd/matching-engine
	@echo "✅ 編譯完成: ./matching-engine"

build-market-data-service: ## 編譯 market-data-service
	@echo "📦 編譯 market-data-service..."
	go build -o $(BUILD_DIR)/market-data-service ./cmd/market-data-service
	@echo "✅ 編譯完成: ./market-data-service"

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
	@echo "> k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env SYMBOL=$(SYMBOL) scripts/k6/smoke-test.js"
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env SYMBOL=$(SYMBOL) scripts/k6/smoke-test.js

test-coverage: ## 執行測試並產生覆蓋率報告
	@echo "📊 產生測試覆蓋率..."
	go test -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html
	@echo "✅ 覆蓋率報告: coverage.html"

# --- Docker 開發環境 (Air 熱重載) ---

dev-up: ## 啟動開發環境 (含 Air 熱重載)
	@echo "🚀 啟動開發環境 (Air，預設假設 infra 已由外部容器提供)..."
	docker compose -f docker-compose.dev.yml up -d

dev-down: ## 停止開發環境
	@echo "🛑 停止開發環境..."
	docker compose -f docker-compose.dev.yml down

dev-build: ## 編譯 Docker 鏡像
	@echo "🐳 編譯 Docker 鏡像..."
	docker compose -f docker-compose.dev.yml up -d --build

dev-logs: ## 查看開發環境日誌
	docker compose -f docker-compose.dev.yml logs -f ${SERVICE_NAME:-gateway}

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
	rm -f $(BUILD_DIR)/gateway
	rm -f $(BUILD_DIR)/order-service
	rm -f $(BUILD_DIR)/matching-engine
	rm -f $(BUILD_DIR)/market-data-service
	rm -f coverage.txt coverage.html
	@echo "✅ 清理完成"

# --- ☁️  AWS 雲端基礎開發 (Terraform) ---

ENVIRONMENT ?= staging
AWS_REGION ?= ap-northeast-1
TF_DIR = deploy/terraform/environments/$(ENVIRONMENT)
ECSPRESSO_DIR = deploy/ecspresso/monolith

infra-init: ## 初始化 Terraform 基礎建設
	@echo "🏗️  初始化 Terraform ($(ENVIRONMENT))..."
	cd $(TF_DIR) && terraform init

infra-plan: ## 預覽待變動的雲端資源
	@echo "📋 預覽雲端資源變動..."
	cd $(TF_DIR) && terraform plan -input=false -out=tfplan

infra-apply: ## 正式執行並更新雲端基礎建設
	@echo "🚀 正式套用雲端基礎建設變動..."
	@echo "注意：這將產生 AWS 費用。"
	cd $(TF_DIR) && terraform apply tfplan

infra-destroy: ## 移除所有雲端基礎建設 (需加 CONFIRM=1)
ifeq ($(CONFIRM),1)
	@echo "🧨 刪除 $(ENVIRONMENT) 環境所有雲端資源..."
	cd $(TF_DIR) && terraform destroy -auto-approve
else
	@echo "⚠️  警告：這是危險操作！請執行 'make infra-destroy CONFIRM=1' 來確認刪除。"
	@exit 1
endif

# --- 🐳  鏡像管理與 AWS ECR ---

aws-login: ## 登入 AWS ECR
	@echo "🔑 登入 AWS ECR..."
	aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $$(cd $(TF_DIR) && terraform output -raw ecr_repository_url | cut -d'/' -f1)

docker-build-push: aws-login ## 編譯並推送 Docker 鏡像至 AWS ECR
	@echo "🐳 編譯 Docker 鏡像..."
	docker build -t $$(cd $(TF_DIR) && terraform output -raw ecr_repository_url):latest .
	@echo "📤 推送鏡像至 ECR..."
	docker push $$(cd $(TF_DIR) && terraform output -raw ecr_repository_url):latest

# --- 🚀  應用程式部署 (ecspresso) ---

ecs-deploy: ## 使用 ecspresso 部署應用程式至 ECS
	@echo "🚀 部署應用程式至 ECS Fargate ($(ENVIRONMENT))..."
	export ECR_IMAGE=$$(cd $(TF_DIR) && terraform output -raw ecr_repository_url):latest && \
	cd $(ECSPRESSO_DIR) && ecspresso deploy --config ecspresso.yml

ecs-rollback: ## 快速回滾至上一個穩定版本
	@echo "⏪ 回滾 ECS 服務..."
	cd $(ECSPRESSO_DIR) && ecspresso rollback --config ecspresso.yml

ecs-status: ## 查看目前 ECS 服務與任務狀態
	@echo "📊 查看 ECS 狀態..."
	cd $(ECSPRESSO_DIR) && ecspresso status --config ecspresso.yml --events 10

ecs-logs: ## 查看雲端 CloudWatch 即時日誌 (Tail)
	@echo "📝 查看即時日誌..."
	aws logs tail --region $(AWS_REGION) --follow --since 5m /ecs/exchange/$(ENVIRONMENT)/monolith

ecs-exec: ## 進入運行的 Fargate 容器執行指令 (類似 docker exec)
	@echo "🐚 啟動互動式 Shell 進入容器..."
	cd $(ECSPRESSO_DIR) && ecspresso exec --config ecspresso.yml --command /bin/sh

# --- 🏆  一鍵完整流程 ---

deploy-all: infra-apply docker-build-push ecs-deploy ## [完整佈署] 基礎建設 + 鏡像推送 + ECS 更新

destroy-all: ## [完整刪除] 刪除 ECS 服務 + 基礎設施 (需加 CONFIRM=1)
ifeq ($(CONFIRM),1)
	@echo "🧨 準備完全卸載雲端環境..."
	-cd $(ECSPRESSO_DIR) && ecspresso delete --config ecspresso.yml --force
	$(MAKE) infra-destroy CONFIRM=1
else
	@echo "⚠️  警告：這會刪除整個雲端環境！請執行 'make destroy-all CONFIRM=1'。"
	@exit 1
endif

.DEFAULT_GOAL := help
