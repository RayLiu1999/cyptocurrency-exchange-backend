# 05 — 現行微服務部署契約

本文件是 ECS 重整期間的現行部署契約。
凡是與 AWS ECS 服務拆分、環境變數、內部 DNS、對外暴露方式有關的決策，先以本文件為準，再往後落到 `deploy/ecspresso/` 與 Terraform task/service definitions。

## 契約來源

| 類型 | 來源 |
|------|------|
| **服務入口** | `cmd/gateway/main.go`、`cmd/order-service/main.go`、`cmd/matching-engine/main.go`、`cmd/market-data-service/main.go`、`cmd/simulation-service/main.go` |
| **基礎設施** | `deploy/terraform/environments/staging/main.tf`、`deploy/terraform/modules/*` |
| **測試與驗證** | `docs/testing/ECS_TESTING.md`、`docs/testing/TEST_EXECUTION_RUNBOOK.md`、`docs/testing/TEST_REPORT_TEMPLATE.md` |

## 現行拓樸

| 服務 | 主要責任 | Port | 對外暴露 | 啟動依賴 | ECS 部署角色 |
|------|----------|------|----------|----------|--------------|
| **gateway** | HTTP / WebSocket 單一入口，反向代理 order-service、market-data-service、simulation-service | `8100` | 是，經 ALB | order-service、market-data-service、Redis 可選 | 對外 API entrypoint |
| **order-service** | 下單、資金鎖定、Outbox 補發、settlement consumer | `8103` | 否 | PostgreSQL、Kafka，Redis 建議 | 內部 HTTP + Kafka consumer |
| **matching-engine** | 記憶體撮合、leader election、snapshot restore、行情事件發布 | `8101` | 否 | PostgreSQL、Kafka，Redis 建議 | 內部 worker |
| **market-data-service** | 行情查詢、WebSocket 推播、Kafka market event consumer | `8102` | 否 | PostgreSQL、Kafka，Redis 建議 | 內部 HTTP + WebSocket upstream |
| **simulation-service** | 壓測控制面 API，透過 gateway 驅動模擬流量 | `8104` | 否 | gateway | 非首輪 blocking service |
| **redpanda** | Kafka broker | `9092` | 否 | EFS、ECS | 內部訊息基礎設施 |

## 流量與服務發現契約

### 對外流量

| 類型 | 路徑 | 入口 |
|------|------|------|
| **Health** | `/health` | `gateway` 經 ALB |
| **HTTP API** | `/api/v1/*` | `gateway` |
| **WebSocket** | `/ws` | `gateway -> market-data-service` |
| **Swagger / docs** | `/swagger/*`、`/docs/*` | `gateway -> order-service` |

### 內部 DNS 契約

本段的目的，是先固定「ECS 內部服務彼此要用什麼名稱找到對方」。
它記錄的是 **服務發現用的穩定位址**，不是所有內部元件、所有模組、或所有 AWS 資源的總表。

白話來說，這份契約是在回答兩個問題：

1. 某個內部服務在叢集裡應該叫什麼名字。
2. 其他服務如果要連它，應該打哪個 port。

在 ECS / Fargate 環境裡，task 的私有 IP 不是固定的，滾動更新、重建、擴容後都可能改變。
因此應用程式不應直接依賴容器 IP，而是依賴一個固定的內部 DNS 名稱。

`exchange.internal` 是這個專案預計使用的 **private DNS namespace**，用途類似一個只存在於 VPC 內部的通訊錄。
像 `order-service.exchange.internal`、`market-data-service.exchange.internal` 這類名稱，背後會由 ECS Service Discovery / AWS Cloud Map 對應到當下健康中的 task IP。
也就是說，應用程式只要記住服務名稱，不需要記住容器實際跑在哪一台機器上。

本表只收錄符合下列條件的端點：

- 這是一個獨立的網路服務，而不是程式內部 package 或模組。
- 其他服務會用 `hostname:port` 的方式主動連線到它。
- 這個名稱需要成為長期固定的部署契約，供 Terraform、ecspresso、task definition 與應用程式設定共同使用。

因此，像 `gateway`、`order-service`、`market-data-service` 這類 HTTP upstream 需要列進來；
`redpanda` 也需要列進來，因為多個服務都要用一致的 broker 名稱連到 Kafka。

相對地，Redis 與 PostgreSQL 在目前契約中**不以 `exchange.internal` 名稱直接暴露給應用程式使用**，原因是它們比較適合透過連線字串管理，而不是只靠 service discovery 名稱：

- 應用程式實際需要的是完整 `REDIS_URL` / `DATABASE_URL`，而不是只有 host 名稱。
- 連線字串通常還會包含 schema、帳號、密碼、SSL 參數、db name、連線選項等資訊，這些資訊無法只靠 DNS 名稱表達。
- 這類資料屬於敏感設定，應集中放在 SSM Parameter Store，讓 task definition 以環境變數注入，避免把連線細節散落在多份部署檔案裡。
- 若未來 Redis 或 PostgreSQL 的實際端點、驗證方式或連線參數變更，只需更新 SSM 值，不需要同步修改每個服務的應用程式設定。

> ECS 微服務版本沿用現有 private DNS namespace 方向，與 `redpanda.exchange.internal` 保持一致。

| 服務 | 建議內部位址 | 用途 |
|------|--------------|------|
| **gateway** | `http://gateway.exchange.internal:8100` | 內部除錯或 simulation-service 反向呼叫時使用 |
| **matching-engine** | `http://matching-engine.exchange.internal:8101` | 內部監控與 Health Check |
| **order-service** | `http://order-service.exchange.internal:8103` | gateway upstream |
| **market-data-service** | `http://market-data-service.exchange.internal:8102` | gateway HTTP / WebSocket upstream |
| **simulation-service** | `http://simulation-service.exchange.internal:8104` | gateway upstream，非首輪必備 |
| **redpanda** | `redpanda.exchange.internal:9092` | Kafka brokers |

## 環境變數契約

### gateway

| 變數 | 必要性 | 建議值 / 來源 | 說明 |
|------|--------|---------------|------|
| `GATEWAY_PORT` | 選填 | `8100` | 容器 listen port |
| `ORDER_SERVICE_URL` | 必填 | `http://order-service.exchange.internal:8103` | order-service upstream |
| `MARKET_DATA_SERVICE_URL` | 必填 | `http://market-data-service.exchange.internal:8102` | market-data-service upstream |
| `SIMULATION_SERVICE_URL` | 選填 | `http://simulation-service.exchange.internal:8104` | simulation upstream，首輪可先不注入 |
| `REDIS_URL` | 選填 | `SSM: /exchange/staging/REDIS_URL` | 限流與冪等性儲存，未提供時退回 memory mode |

### order-service

| 變數 | 必要性 | 建議值 / 來源 | 說明 |
|------|--------|---------------|------|
| `ORDER_SERVICE_PORT` | 選填 | `8103` | 容器 listen port |
| `DATABASE_URL` | 必填 | `SSM: /exchange/staging/DATABASE_URL` | PostgreSQL 連線 |
| `REDIS_URL` | 建議 | `SSM: /exchange/staging/REDIS_URL` | 市價單估算與快取 |
| `KAFKA_BROKERS` | 必填 | `SSM: /exchange/staging/KAFKA_BROKERS` 或 `redpanda.exchange.internal:9092` | Kafka brokers |
| `KAFKA_RESET_OFFSET` | 選填 | `latest` | 避免誤重播歷史事件 |
| `GIN_MODE` | 必填 | `release` | 正式環境模式 |

### matching-engine

| 變數 | 必要性 | 建議值 / 來源 | 說明 |
|------|--------|---------------|------|
| `MATCHING_ENGINE_PORT` | 選填 | `8101` | health / metrics port |
| `DATABASE_URL` | 必填 | `SSM: /exchange/staging/DATABASE_URL` | snapshot restore + leader election |
| `REDIS_URL` | 建議 | `SSM: /exchange/staging/REDIS_URL` | 掛單簿快取同步 |
| `KAFKA_BROKERS` | 必填 | `SSM: /exchange/staging/KAFKA_BROKERS` 或 `redpanda.exchange.internal:9092` | Kafka brokers |
| `KAFKA_RESET_OFFSET` | 選填 | `latest` | 無 committed offset 時的安全預設 |
| `KAFKA_ALLOW_AUTO_CREATE` | 必填 | `false` | 避免正式環境自動建 topic |

### market-data-service

| 變數 | 必要性 | 建議值 / 來源 | 說明 |
|------|--------|---------------|------|
| `MARKET_DATA_PORT` | 選填 | `8102` | 容器 listen port |
| `DATABASE_URL` | 必填 | `SSM: /exchange/staging/DATABASE_URL` | 查詢服務資料來源 |
| `REDIS_URL` | 建議 | `SSM: /exchange/staging/REDIS_URL` | orderbook cache |
| `KAFKA_BROKERS` | 必填 | `SSM: /exchange/staging/KAFKA_BROKERS` 或 `redpanda.exchange.internal:9092` | Kafka brokers |
| `KAFKA_RESET_OFFSET` | 選填 | `latest` | 避免歷史事件重播 |

### simulation-service

| 變數 | 必要性 | 建議值 / 來源 | 說明 |
|------|--------|---------------|------|
| `SIMULATION_SERVICE_PORT` | 選填 | `8104` | 容器 listen port |
| `GATEWAY_URL` | 必填 | `http://gateway.exchange.internal:8100` | 模擬請求入口 |

## SSM / Parameter Store 契約

| 參數名稱 | 類型 | 使用服務 | 備註 |
|----------|------|----------|------|
| `/exchange/staging/DATABASE_URL` | `SecureString` | order-service、matching-engine、market-data-service | 由 data module 建立 |
| `/exchange/staging/REDIS_URL` | `SecureString` | gateway、order-service、matching-engine、market-data-service | gateway 可選，其他服務建議 |
| `/exchange/staging/KAFKA_BROKERS` | `String` | order-service、matching-engine、market-data-service | 由 messaging module 建立 |
| `/exchange/staging/GIN_MODE` | `String` | order-service | staging / production 應固定 `release` |
| `/exchange/staging/KAFKA_ALLOW_AUTO_CREATE` | `String` | matching-engine | 預設 `false` |

> `KAFKA_RESET_OFFSET` 不建議做成全域 SSM 預設值。此參數應保持 task definition 顯式控制，並以 `latest` 為預設，只有在受控 replay / recovery 測試時才覆寫。

## Health / Metrics 契約

| 服務 | Health Path | Metrics Path | 是否掛 ALB |
|------|-------------|--------------|------------|
| **gateway** | `/health` | `/metrics` | 是 |
| **order-service** | `/health` | `/metrics` | 否 |
| **matching-engine** | `/health` | `/metrics` | 否 |
| **market-data-service** | `/health` | `/metrics` | 否 |
| **simulation-service** | `/health` | `/metrics` | 否 |

## 首輪 ECS 部署決策

| 項目 | 決策 |
|------|------|
| **對外 Target Group** | 只掛 `gateway` |
| **gateway 副本數** | `1 -> 2`，先單副本驗證，再視壓測結果提升 |
| **order-service 副本數** | `1`，先避免 outbox / consumer 行為在未驗證前擴散 |
| **matching-engine 副本數** | `1` active，待後續再驗證 standby failover |
| **market-data-service 副本數** | `1`，先避免 WebSocket stickiness 問題 |
| **simulation-service** | 非首輪 blocking scope |

## 目前已知限制

| 項目 | 現況 |
|------|------|
| **legacy monolith ecspresso** | 仍以 local `terraform.tfstate` 為前提，只供歷史參考 |
| **microservice ecspresso 對接** | 已改為由 Terraform outputs 匯出 role、subnet、security group、service registry 與 SSM ARN；待 staging 實際部署驗證 |
| **KAFKA_BROKERS / DATABASE_URL / REDIS_URL 的輸出介面** | 已可供 per-service task definition 直接引用，待後續 staging 驗證實際注入結果 |

## 下一步

- 依本契約執行 ECS-04，將 staging Terraform outputs 與 SSM mapping 補齊到可供 task definitions 直接引用。
- 依本契約執行 ECS-05 / ECS-06，建立 gateway、order-service、matching-engine、market-data-service 的 ECS 定義。