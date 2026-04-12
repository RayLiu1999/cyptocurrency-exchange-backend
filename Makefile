.PHONY: build build-server build-gateway build-order-service build-matching-engine build-market-data-service build-simulation-service test test-integration test-race test-all test-smoke test-load test-spike test-coverage lint vuln fmt tidy clean dev-up dev-down dev-logs test-up test-down test-build test-logs test-infra-up test-infra-down db-migrate db-seed db-fresh bootstrap-init bootstrap-plan bootstrap-apply aws-login docker-build-push docker-build-push-core infra-init infra-plan infra-apply infra-destroy show-staging-outputs ecs-create ecs-delete ecs-deploy ecs-rollback ecs-status ecs-status-all ecs-logs ecs-exec ecs-create-core ecs-deploy-core ecs-delete-core staging-create-core staging-rollout-core staging-health staging-smoke-test staging-load-test staging-spike-test staging-baseline-test deploy-all destroy-all check-ecs-service help

# 變數定義
BUILD_DIR=.
DB_USER=postgres
DB_PASSWORD?=123qwe
DB_NAME=exchange
REDIS_PASSWORD?=123qwe
DB_CONTAINER ?= postgres
BASE_URL ?= http://localhost:8100/api/v1
WS_URL ?= ws://localhost:8100/ws
SYMBOL ?= BTC-USD

# 測試環境專用變數 (優先於 .env，避免開發環境汙染)
GIN_MODE ?= test
DATABASE_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@localhost:5432/$(DB_NAME)?sslmode=disable

# 基礎設施組態檔路徑
INFRA_COMPOSE_FILE = deploy/docker-compose.test-infra.yml

K6_ENV_FLAGS ?=
TERRAFORM_INIT_FLAGS ?=
IMAGE_TAG ?= latest
SUPPORTED_ECS_SERVICES = gateway order-service matching-engine market-data-service
CORE_ECS_SERVICES         = matching-engine order-service market-data-service gateway
TEARDOWN_ECS_SERVICES     = gateway market-data-service order-service matching-engine

# 載入 .env 檔案並匯出為環境變數 (Makefile 變數優先於 .env)
ifneq (,$(wildcard .env))
    include .env
    export $(shell sed 's/=.*//' .env)
endif

help: ## 顯示所有可用指令
	@echo "可用指令:"
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# ==============================================================================
# 微服務編譯 (Local Build)
# ==============================================================================

build: build-gateway build-order-service build-matching-engine build-market-data-service build-simulation-service ## 編譯所有微服務
	@echo "✅ 所有微服務編譯完成"

build-server: ## (已退役) Legacy 單體版本入口
	@echo "❌ build-server 為 legacy monolith 指令；請改用 'make build'。"
	@exit 1

build-gateway: ## 編編 gateway
	@echo "📦 編譯 gateway..."
	go build -o $(BUILD_DIR)/gateway ./cmd/gateway

build-order-service: ## 編譯 order-service
	@echo "📦 編譯 order-service..."
	go build -o $(BUILD_DIR)/order-service ./cmd/order-service

build-matching-engine: ## 編譯 matching-engine
	@echo "📦 編譯 matching-engine..."
	go build -o $(BUILD_DIR)/matching-engine ./cmd/matching-engine

build-market-data-service: ## 編譯 market-data-service
	@echo "📦 編譯 market-data-service..."
	go build -o $(BUILD_DIR)/market-data-service ./cmd/market-data-service

build-simulation-service: ## 編譯 simulation-service
	@echo "📦 編譯 simulation-service..."
	go build -o $(BUILD_DIR)/simulation-service ./cmd/simulation-service

# ==============================================================================
# 測試自動化 (Testing)
# ==============================================================================

test: ## 執行單元測試
	@echo "🧪 執行基礎單元測試..."
	@GIN_MODE=$(GIN_MODE) DATABASE_URL="$(DATABASE_URL)" go test -v ./...

test-integration: ## 執行整合測試 (含 DB/併發測試)
	@echo "🔥 執行整合與併發測試 (tags: integration)..."
	@GIN_MODE=$(GIN_MODE) DATABASE_URL="$(DATABASE_URL)" go test -v -tags=integration ./internal/infrastructure/election/... ./internal/repository/... ./internal/infrastructure/outbox/...

test-race: ## 執行競態偵測測試
	@echo "🏎️  執行競態偵測測試..."
	@GIN_MODE=$(GIN_MODE) DATABASE_URL="$(DATABASE_URL)" go test -v -race ./...

test-all: ## 執行完整套件 (Unit, Integration, Race)
	@echo "🚀 執行完整測試套件..."
	@GIN_MODE=$(GIN_MODE) DATABASE_URL="$(DATABASE_URL)" go test -v -race -tags=integration ./...

test-coverage: ## 產生測試覆蓋率報告
	@echo "📊 產生測試覆蓋率..."
	@GIN_MODE=$(GIN_MODE) DATABASE_URL="$(DATABASE_URL)" go test -coverprofile=coverage.txt -covermode=atomic ./...
	@go tool cover -html=coverage.txt -o coverage.html
	@echo "✅ 覆蓋率報告: coverage.html"

# ==============================================================================
# 測試用設施管理 (Infrastructure for Testing/CI)
# ==============================================================================

test-infra-up: ## 啟動基礎設施 (Postgres, Redis, Kafka)
	@echo "🚀 啟動基礎設施..."
	@docker compose -f $(INFRA_COMPOSE_FILE) up -d
	@echo "⏳ 等待資料庫 Ready..."
	@until docker exec $(DB_CONTAINER) pg_isready -U $(DB_USER) -d $(DB_NAME) > /dev/null 2>&1; do \
		echo "Waiting for database..."; \
		sleep 2; \
	done
	@echo "✅ 基礎設施已啟動"

test-infra-down: ## 停止基礎設施
	@echo "🛑 停止基礎設施..."
	@docker compose -f $(INFRA_COMPOSE_FILE) down

# ==============================================================================
# 資料庫操作 (Database)
# ==============================================================================

db-migrate: ## 執行資料庫 Migration (sql/schema.sql)
	@echo "📊 執行 Migration..."
	@docker exec -i $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) < sql/schema.sql
	@echo "✅ Migration 完成"

db-seed: ## 插入測試種子資料 (sql/seed.sql)
	@echo "🌱 插入測試資料..."
	@docker exec -i $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) < sql/seed.sql
	@echo "✅ 測試資料插入完成"

db-fresh: ## 清空並重建資料庫結構
	@echo "🧹 清空並重建 Public Schema..."
	@docker exec -i $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
	@$(MAKE) db-migrate
	@echo "✅ 資料庫表結構已清空並重建"

# ==============================================================================
# K6 壓力測試 (Load Testing)
# ==============================================================================

test-smoke: ## K6: 核心交易冒煙測試
	@echo "🔥 執行 k6 核心冒煙測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env SYMBOL=$(SYMBOL) scripts/k6/smoke-test.js

test-load: ## K6: 負載測試
	@echo "🔥 執行 k6 負載測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) scripts/k6/load-test.js

test-spike: ## K6: 尖峰測試
	@echo "🔥 執行 k6 尖峰測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) scripts/k6/spike-test.js

test-e2e-latency: ## K6: 端到端延遲測試 (HTTP -> Kafka -> WS)
	@echo "⚡ 執行 k6 端到端延遲測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env WS_URL=$(WS_URL) scripts/k6/e2e-latency-test.js

test-capacity: ## K6: 撮合引擎極限容量測試
	@echo "🚀 執行 k6 容量測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env SYMBOL=$(SYMBOL) scripts/k6/matching-engine-capacity-test.js

test-market-storm: ## K6: 行情風暴測試 (WS 吞吐量)
	@echo "🌪️  執行 k6 行情風暴測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env WS_URL=$(WS_URL) scripts/k6/market-storm-test.js

test-hot-symbol: ## K6: 單一熱門交易對測試
	@echo "🔥 執行 k6 單一熱門交易對測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env SYMBOL_MODE=hot scripts/k6/hot-vs-multi-symbol-test.js

test-multi-symbol: ## K6: 多交易對測試 (Kafka 分流驗證)
	@echo "☄️  執行 k6 多交易對測試..."
	@k6 run $(K6_ENV_FLAGS) --env BASE_URL=$(BASE_URL) --env SYMBOL_MODE=multi scripts/k6/hot-vs-multi-symbol-test.js

# ==============================================================================
# 開發與品質管理 (Development)
# ==============================================================================

dev-up: ## 啟動開發環境 (Air 熱重載)
	@echo "🚀 啟動開發環境 (Air)..."
	docker compose -f docker-compose.dev.yml up -d

dev-down: ## 停止開發環境
	docker compose -f docker-compose.dev.yml down

lint: ## 執行程式碼檢查 (golangci-lint)
	@echo "🔍 執行程式碼檢查..."
	golangci-lint run ./...

vuln: ## 檢查安全性漏洞 (govulncheck)
	@echo "🔍 檢查漏洞..."
	govulncheck ./...

fmt: ## 格式化程式碼
	@echo "✨ 格式化程式碼..."
	go fmt ./...

tidy: ## 整理依賴 (go mod tidy)
	@echo "📦 整理依賴..."
	go mod tidy

clean: ## 清理編譯檔案與測試快取
	@echo "🧹 清理檔案..."
	rm -f $(BUILD_DIR)/gateway $(BUILD_DIR)/order-service $(BUILD_DIR)/matching-engine $(BUILD_DIR)/market-data-service $(BUILD_DIR)/simulation-service
	rm -f coverage.txt coverage.html
	@echo "✅ 清理完成"

# --- ☁️  AWS 雲端基礎開發 (Terraform) ---

ENVIRONMENT ?= staging
AWS_REGION ?= ap-northeast-1
TF_DIR = deploy/terraform/environments/$(ENVIRONMENT)
BOOTSTRAP_DIR = deploy/terraform/bootstrap
ECS_SERVICE ?= gateway
ECR_IMAGE_TAG ?= $(ECS_SERVICE)-$(IMAGE_TAG)
ECSPRESSO_DIR = deploy/ecspresso
ECS_LOG_GROUP ?= /ecs/exchange/$(ENVIRONMENT)/$(ECS_SERVICE)

define ecspresso_env_exports
export AWS_REGION=$(AWS_REGION) && \
export ECS_CLUSTER_NAME=$$(cd $(TF_DIR) && terraform output -raw ecs_cluster_name) && \
export TASK_EXECUTION_ROLE_ARN=$$(cd $(TF_DIR) && terraform output -raw task_execution_role_arn) && \
export TASK_ROLE_ARN=$$(cd $(TF_DIR) && terraform output -raw task_role_arn) && \
export PRIVATE_SUBNET_IDS_JSON=$$(cd $(TF_DIR) && terraform output -json private_subnet_ids) && \
export ECS_SECURITY_GROUP_JSON=$$(cd $(TF_DIR) && terraform output -json sg_ecs_id) && \
export TARGET_GROUP_ARN=$$(cd $(TF_DIR) && terraform output -raw target_group_arn) && \
export DATABASE_URL_SSM_ARN=$$(cd $(TF_DIR) && terraform output -raw database_url_ssm_arn) && \
export REDIS_URL_SSM_ARN=$$(cd $(TF_DIR) && terraform output -raw redis_url_ssm_arn) && \
export KAFKA_BROKERS_SSM_ARN=$$(cd $(TF_DIR) && terraform output -raw kafka_brokers_ssm_arn) && \
export GIN_MODE_SSM_ARN=$$(cd $(TF_DIR) && terraform output -raw gin_mode_ssm_arn) && \
export KAFKA_ALLOW_AUTO_CREATE_SSM_ARN=$$(cd $(TF_DIR) && terraform output -raw kafka_allow_auto_create_ssm_arn) && \
export GATEWAY_SERVICE_REGISTRY_ARN=$$(cd $(TF_DIR) && terraform output -raw gateway_service_registry_arn) && \
export ORDER_SERVICE_REGISTRY_ARN=$$(cd $(TF_DIR) && terraform output -raw order_service_service_registry_arn) && \
export MATCHING_ENGINE_REGISTRY_ARN=$$(cd $(TF_DIR) && terraform output -raw matching_engine_service_registry_arn) && \
export MARKET_DATA_SERVICE_REGISTRY_ARN=$$(cd $(TF_DIR) && terraform output -raw market_data_service_service_registry_arn) && \
export SIMULATION_SERVICE_REGISTRY_ARN=$$(cd $(TF_DIR) && terraform output -raw simulation_service_service_registry_arn) && \
export ECR_IMAGE=$$(cd $(TF_DIR) && terraform output -raw ecr_repository_url):$(ECR_IMAGE_TAG)
endef

check-ecs-service:
ifeq ($(filter $(ECS_SERVICE),$(SUPPORTED_ECS_SERVICES)),)
	@echo "❌ ECS_SERVICE=$(ECS_SERVICE) 不在支援清單內。請使用: $(SUPPORTED_ECS_SERVICES)"
	@exit 1
endif

bootstrap-init: ## 初始化 Terraform remote state bootstrap
	@echo "🏗️  初始化 Terraform bootstrap..."
	cd $(BOOTSTRAP_DIR) && terraform init $(TERRAFORM_INIT_FLAGS)

bootstrap-plan: ## 預覽 Terraform remote state bootstrap 資源
	@echo "📋 預覽 bootstrap 資源變動..."
	cd $(BOOTSTRAP_DIR) && terraform plan -input=false -out=tfplan

bootstrap-apply: ## 套用 Terraform remote state bootstrap 資源
	@echo "🚀 建立 remote state 所需的 S3 bucket 與 DynamoDB lock table..."
	@echo "注意：這將產生 AWS 費用。"
	cd $(BOOTSTRAP_DIR) && terraform apply tfplan

infra-init: ## 初始化 Terraform 基礎建設
	@echo "🏗️  初始化 Terraform ($(ENVIRONMENT))..."
	cd $(TF_DIR) && terraform init $(TERRAFORM_INIT_FLAGS)

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

show-staging-outputs: ## 顯示 staging ALB、API 與 WebSocket 入口
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	echo "ALB_DNS=$$ALB_DNS"; \
	echo "BASE_URL=http://$$ALB_DNS/api/v1"; \
	echo "WS_URL=ws://$$ALB_DNS/ws"; \
	echo "ECS_CLUSTER_NAME=$$(cd $(TF_DIR) && terraform output -raw ecs_cluster_name)"; \
	echo "ECR_REPOSITORY_URL=$$(cd $(TF_DIR) && terraform output -raw ecr_repository_url)"

# --- 🐳  鏡像管理與 AWS ECR ---

aws-login: ## 登入 AWS ECR
	@echo "🔑 登入 AWS ECR..."
	aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $$(cd $(TF_DIR) && terraform output -raw ecr_repository_url | cut -d'/' -f1)

docker-build-push: aws-login check-ecs-service ## 編譯並推送指定微服務 Docker 鏡像至 AWS ECR
	@echo "🐳 編譯 Docker 鏡像 (service=$(ECS_SERVICE), tag=$(ECR_IMAGE_TAG))..."
	docker build --build-arg SERVICE_NAME=$(ECS_SERVICE) -t $$(cd $(TF_DIR) && terraform output -raw ecr_repository_url):$(ECR_IMAGE_TAG) .
	@echo "📤 推送鏡像至 ECR..."
	docker push $$(cd $(TF_DIR) && terraform output -raw ecr_repository_url):$(ECR_IMAGE_TAG)

docker-build-push-core: aws-login ## 依序推送四個核心微服務鏡像
	@for svc in $(CORE_ECS_SERVICES); do \
		printf "\n==> build/push %s\n" "$$svc"; \
		$(MAKE) docker-build-push ECS_SERVICE=$$svc IMAGE_TAG=$(IMAGE_TAG) || exit $$?; \
	done

# --- 🚀  應用程式部署 (ecspresso) ---

ecs-create: check-ecs-service ## 首次建立 ECS 服務
	@echo "🆕 建立 ECS 服務 ($(ENVIRONMENT)，service=$(ECS_SERVICE))..."
	@echo "使用映像標籤: $(ECR_IMAGE_TAG)"
	@$(ecspresso_env_exports) && \
	cd deploy/ecspresso/$(ECS_SERVICE) && ecspresso deploy --config ecspresso.yml

ecs-deploy: check-ecs-service ## 使用 ecspresso 部署應用程式至 ECS
	@echo "🚀 部署應用程式至 ECS Fargate ($(ENVIRONMENT)，service=$(ECS_SERVICE))..."
	@echo "使用映像標籤: $(ECR_IMAGE_TAG)"
	@$(ecspresso_env_exports) && \
	cd deploy/ecspresso/$(ECS_SERVICE) && ecspresso deploy --config ecspresso.yml

ecs-delete: check-ecs-service ## 刪除指定 ECS 服務
	@echo "🗑️  刪除 ECS 服務 ($(ECS_SERVICE))..."
	@$(ecspresso_env_exports) && \
	cd deploy/ecspresso/$(ECS_SERVICE) && ecspresso delete --config ecspresso.yml --force

ecs-rollback: check-ecs-service ## 快速回滾至上一個穩定版本
	@echo "⏪ 回滾 ECS 服務 ($(ECS_SERVICE))..."
	@$(ecspresso_env_exports) && \
	cd deploy/ecspresso/$(ECS_SERVICE) && ecspresso rollback --config ecspresso.yml

ecs-status: check-ecs-service ## 查看目前 ECS 服務與任務狀態
	@echo "📊 查看 ECS 狀態 ($(ECS_SERVICE))..."
	@$(ecspresso_env_exports) && \
	cd deploy/ecspresso/$(ECS_SERVICE) && ecspresso status --config ecspresso.yml --events 10

ecs-status-all: ## 依部署順序檢查四個核心服務狀態
	@for svc in $(CORE_ECS_SERVICES); do \
		printf "\n==> status %s\n" "$$svc"; \
		$(MAKE) ecs-status ECS_SERVICE=$$svc || exit $$?; \
	done

ecs-logs: ## 查看雲端 CloudWatch 即時日誌 (Tail)
	@echo "📝 查看即時日誌 ($(ECS_LOG_GROUP))..."
	aws logs tail --region $(AWS_REGION) --follow --since 5m $(ECS_LOG_GROUP)

ecs-exec: check-ecs-service ## 進入運行的 Fargate 容器執行指令 (類似 docker exec)
	@echo "🐚 啟動互動式 Shell 進入容器 ($(ECS_SERVICE))..."
	@$(ecspresso_env_exports) && \
	cd $(ECSPRESSO_DIR)/$(ECS_SERVICE) && ecspresso exec --config ecspresso.yml --command /bin/sh

ecs-create-core: ## 依序首次建立四個核心微服務
	@for svc in $(CORE_ECS_SERVICES); do \
		printf "\n==> create %s\n" "$$svc"; \
		$(MAKE) ecs-create ECS_SERVICE=$$svc IMAGE_TAG=$(IMAGE_TAG) || exit $$?; \
	done

ecs-deploy-core: ## 依序更新四個核心微服務
	@for svc in $(CORE_ECS_SERVICES); do \
		printf "\n==> deploy %s\n" "$$svc"; \
		$(MAKE) ecs-deploy ECS_SERVICE=$$svc IMAGE_TAG=$(IMAGE_TAG) || exit $$?; \
	done

ecs-delete-core: ## 依逆向順序刪除四個核心微服務
	@for svc in $(TEARDOWN_ECS_SERVICES); do \
		printf "\n==> delete %s\n" "$$svc"; \
		$(MAKE) ecs-delete ECS_SERVICE=$$svc IMAGE_TAG=$(IMAGE_TAG) || echo "⚠️  略過 $$svc（可能尚未建立或已刪除）"; \
	done

staging-create-core: docker-build-push-core ecs-create-core ## 首次建立四個核心微服務（需先完成 infra-apply）

staging-rollout-core: infra-apply docker-build-push-core ecs-deploy-core ## 套用 infra 後依序更新四個核心微服務

staging-health: ## 透過 ALB 檢查 staging gateway health
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	echo "🌡️  檢查 http://$$ALB_DNS/health"; \
	curl --fail --show-error --silent "http://$$ALB_DNS/health"; \
	echo

staging-smoke-test: ## 以 staging ALB 執行 smoke test
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-smoke BASE_URL=http://$$ALB_DNS/api/v1 SYMBOL=$(SYMBOL) K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-load-test: ## 以 staging ALB 執行 load test
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-load BASE_URL=http://$$ALB_DNS/api/v1 SYMBOL=$(SYMBOL) K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-spike-test: ## 以 staging ALB 執行 spike test
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-spike BASE_URL=http://$$ALB_DNS/api/v1 SYMBOL=$(SYMBOL) K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-e2e-latency: ## 以 staging ALB 執行 E2E 延遲測試
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-e2e-latency BASE_URL=http://$$ALB_DNS/api/v1 WS_URL=ws://$$ALB_DNS/ws K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-capacity: ## 以 staging ALB 執行容量測試
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-capacity BASE_URL=http://$$ALB_DNS/api/v1 SYMBOL=$(SYMBOL) K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-market-storm: ## 以 staging ALB 執行行情風暴測試
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-market-storm BASE_URL=http://$$ALB_DNS/api/v1 WS_URL=ws://$$ALB_DNS/ws K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-test-hot-symbol: ## 以 staging ALB 執行熱門交易對測試
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-hot-symbol BASE_URL=http://$$ALB_DNS/api/v1 K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-test-multi-symbol: ## 以 staging ALB 執行多交易對測試
	@ALB_DNS=$$(cd $(TF_DIR) && terraform output -raw alb_dns_name); \
	$(MAKE) test-multi-symbol BASE_URL=http://$$ALB_DNS/api/v1 K6_ENV_FLAGS="$(K6_ENV_FLAGS)"

staging-baseline-test: staging-health staging-smoke-test staging-load-test ## 執行 staging HTTP baseline 驗證


# --- 🏆  一鍵完整流程 ---

deploy-all: infra-apply docker-build-push ecs-deploy ## [單服務完整佈署] 基礎建設 + 鏡像推送 + 指定 ECS_SERVICE 更新

destroy-all: ## [完整刪除] 刪除 ECS 服務 + 基礎設施 (需加 CONFIRM=1)
ifeq ($(CONFIRM),1)
	@echo "🧨 準備完全卸載雲端環境 ($(ENVIRONMENT))..."
	$(MAKE) ecs-delete-core
	$(MAKE) infra-destroy CONFIRM=1
else
	@echo "⚠️  警告：這會刪除整個雲端環境！請執行 'make destroy-all CONFIRM=1'。"
	@exit 1
endif

.DEFAULT_GOAL := help
