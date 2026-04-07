# 09 — Staging 首輪部署故障紀錄與排障手冊

本文件記錄 2026-04 首輪 staging ECS 微服務部署時實際遇到的阻塞問題、根因分析、修復方式與後續預防建議。目標不是保留一次性的對話摘要，而是讓下次部署失敗時可以直接對照症狀快速定位。

> 重要：本輪修復後，staging 的 Redpanda 已改為使用 ECS 任務本地儲存，不再依賴 EFS。若其他歷史文件仍提到 EFS 掛載，請以 Terraform 現行實作與本文件為準。

## 1. 事件摘要

| 編號 | 階段 | 症狀 | 根因 | 處置結果 |
|------|------|------|------|----------|
| 1 | `ecs-create-core` | `ecspresso` template panic，錯誤指向 `VARIABLE` | `ecspresso` 會解析整份 YAML，連註解中的 `{{ must_env VARIABLE }}` 也會被當成模板執行 | 移除註解中的模板示例後恢復正常 |
| 2 | ECS 啟動 Redpanda | service 無法進入 steady state，broker container 啟動失敗 | Redpanda 在 Fargate 上搭配 EFS 時觸發 `io_submit: Operation not supported` | 改為使用容器本地儲存，移除 EFS 掛載 |
| 3 | ECS 健康檢查 | 核心服務被標記為 `UNHEALTHY`，部署卡住或回滾 | container-level health check 過於脆弱，且與實際 readiness 不一致 | 簡化 / 移除 task definition health check，交由 ALB 與服務狀態判斷 |
| 4 | `staging-smoke-test` | `Nothing to be done for 'smoke-test'` | Makefile staging wrapper 呼叫了不存在的 target 名稱 | 修正 staging wrapper 與 `.PHONY` 命名 |
| 5 | k6 smoke test | `JoinArena 未返回 user_id`，實際回應為 `relation "users" does not exist` | staging RDS 已建立但尚未匯入 `sql/schema.sql` | 透過 VPC 模式 CloudShell + S3 中轉匯入 schema 後恢復 |

## 2. 影響範圍

| 元件 | 受影響方式 |
|------|------------|
| `matching-engine` | 首個建立的 service 即卡住，導致後續核心服務無法按順序穩定建立 |
| `order-service` | 即使容器啟動成功，因 RDS schema 缺失而無法建立測試使用者 |
| `market-data-service` | 受 Kafka broker 未就緒與健康檢查連鎖影響 |
| `gateway` | 雖能對外回應 `/health`，但 baseline 測試直到 DB schema 補齊後才完整通過 |
| 驗證流程 | `staging-smoke-test` 在 Makefile target mismatch 與 schema 缺失時都會產生誤導訊號 |

## 3. 問題詳解與修復

### 3.1 ecspresso 解析註解中的模板語法

#### 症狀

- 執行 `make ecs-create-core IMAGE_TAG=<tag>` 時，`ecspresso` 直接 panic。
- 錯誤訊息會指向類似 `function "VARIABLE" not defined`。

#### 根因

`deploy/ecspresso/<service>/ecspresso.yml` 內曾保留示例註解，例如 `{{ must_env VARIABLE }}`。`ecspresso` 解析 YAML 時不會忽略這種註解內的 Go template 語法，導致把示例當成真實模板執行。

#### 修復

- 移除四個核心服務 `ecspresso.yml` 內的模板示例註解。
- 保留純文字說明，不再在註解中放 `{{ ... }}`。

#### 預防

| 原則 | 說明 |
|------|------|
| 註解中禁止保留 Go template 範例 | 只要 `ecspresso` 會讀到的 YAML，都不要放 `{{` 與 `}}` |
| 參數示例改寫成純文字 | 例如改成 `must_env <NAME>`，不要保留可執行模板 |

### 3.2 Redpanda 在 Fargate + EFS 上啟動失敗

#### 症狀

- `matching-engine` 或依賴 Kafka 的服務長時間無法進入 steady state。
- Redpanda CloudWatch logs 出現 `io_submit: Operation not supported`。

#### 根因

Redpanda 在 Fargate 上使用 EFS 作為資料目錄時，底層 I/O 行為與 Redpanda 需要的 AIO 模式不相容。這不是單純的 mount timing 問題，而是運行模型不適配。

#### 修復

- 在 `deploy/terraform/modules/messaging/main.tf` 移除 `efs_volume_configuration` 與對應 `mountPoints`。
- staging 改為接受 Redpanda 使用 ECS 任務本地儲存，重點先放在打通整體交易鏈路與部署流程。

#### 預防

| 原則 | 說明 |
|------|------|
| staging 不要把 Fargate + EFS 當成 Kafka durable storage 解法 | 若需要真正持久化與高可用，應評估 MSK、EC2 自管 Kafka 或其他適合的 broker 方案 |
| 先驗證 broker 實際啟動日誌 | 只看 ECS event 不足以判斷是 health check 問題還是 broker 本身啟動失敗 |

### 3.3 ECS container health check 導致誤判為不健康

#### 症狀

- ECS deployment event 顯示 tasks failed to start 或 container 被標記為 `UNHEALTHY`。
- 即使應用程式稍後可正常回應，部署仍已進入失敗路徑。

#### 根因

首輪 task definition 中的 container-level `healthCheck` 對啟動時間、工具存在性與 readiness 假設過於樂觀。此類檢查在 Fargate 初次冷啟時很容易把暫時性未就緒誤判為永久失敗。

#### 修復

- `Dockerfile` 補入 `curl`，先排除檢查工具不存在的因素。
- 後續直接簡化 / 移除 `deploy/ecspresso/*/ecs-task-def.json` 中過度脆弱的 `healthCheck`，由 ALB health、service event 與應用日誌共同判斷服務狀態。

#### 預防

| 原則 | 說明 |
|------|------|
| container health check 只驗證最小存活條件 | 不要把整個 downstream readiness 綁進去 |
| gateway readiness 交給 ALB | 對外入口應以 ALB target health 為主，不要與 container probe 重複打架 |

### 3.4 staging 測試 wrapper 命名錯置

#### 症狀

- `make staging-smoke-test` 輸出 `make[1]: Nothing to be done for 'smoke-test'`。
- 使用者會誤以為 smoke test 已執行，但實際上沒有跑任何 k6 指令。

#### 根因

Makefile 真正存在的 k6 targets 是 `test-smoke`、`test-load`、`test-spike`、`test-ws-fanout`，但 staging wrapper 呼叫的是 `smoke-test`、`load-test`、`spike-test`、`ws-fanout-test`。

#### 修復

- 修正 `Makefile` 中 `staging-smoke-test`、`staging-load-test`、`staging-spike-test`、`staging-ws-fanout-test` 對應的下游 target。
- 同步修正 `.PHONY` 名稱，避免未來再次出現靜默成功。

#### 預防

| 原則 | 說明 |
|------|------|
| wrapper target 與實際 target 命名需一致 | 若採 `test-*` 前綴，所有文件與 wrapper 都必須統一 |
| baseline 指令至少需能印出實際 `k6 run` 命令 | 避免 `make` 靜默跳過時不易察覺 |

### 3.5 staging RDS 缺少 schema，導致 smoke test 失敗

#### 症狀

- `make staging-smoke-test` 執行後，k6 報 `JoinArena 未返回 user_id`。
- 直接 `curl -X POST <BASE_URL>/test/join` 可看到回應：`ERROR: relation "users" does not exist`。

#### 根因

`make infra-apply` 只負責建立 RDS，不會自動執行 `sql/schema.sql`。因此 `order-service` 雖然能連上資料庫，但資料表不存在。

#### 修復

本輪採用 VPC 模式 CloudShell 執行匯入，透過 S3 作為 SQL 檔案中轉站：

```bash
aws s3 cp s3://<bucket>/<path>/schema.sql ./schema.sql

DB_URL=$(aws ssm get-parameter \
  --region ap-northeast-1 \
  --name "/exchange/staging/DATABASE_URL" \
  --with-decryption \
  --query 'Parameter.Value' \
  --output text)

psql "$DB_URL" -f schema.sql
```

> 若 CloudShell 尚未安裝 `psql`，可先執行 `sudo dnf install -y postgresql16`。

#### 預防

| 原則 | 說明 |
|------|------|
| RDS 建立完成後必須有明確 schema 初始化步驟 | 不能假設 infra apply 會自動建立資料表 |
| 後續應導入正式 migration 機制 | 可評估 `golang-migrate`、one-off ECS task 或 CI/CD migration stage |

## 4. 本輪修復落點

| 檔案 / 模組 | 修復內容 |
|-------------|----------|
| `Makefile` | 修正 staging 測試 wrapper 與 `.PHONY` target 命名 |
| `deploy/ecspresso/*/ecspresso.yml` | 移除註解中的 Go template 示例，避免 `ecspresso` panic |
| `deploy/ecspresso/*/ecs-task-def.json` | 調整 container-level health check 策略 |
| `deploy/terraform/modules/messaging/main.tf` | 移除 Redpanda 的 EFS 掛載，改採本地儲存 |
| `Dockerfile` | 安裝 `curl`，提升容器內診斷與健康檢查可用性 |
| staging RDS | 手動匯入 `sql/schema.sql`，補齊 `users`、`accounts`、`orders`、`trades` 等核心資料表 |

## 5. 建議排障順序

當首次部署再次失敗時，建議依下列順序檢查：

1. 先確認入口資訊：`make show-staging-outputs`
2. 確認四個核心服務狀態：`make ecs-status-all`
3. 若 service 未 steady，先查對應 event 與 logs：`make ecs-status ECS_SERVICE=<service>`、`make ecs-logs ECS_SERVICE=<service>`
4. 若 `/health` 正常但 smoke fail，先直接 `curl` 對應 API 看實際錯誤 body
5. 若錯誤涉及 DB relation 不存在，先檢查 schema 是否已匯入
6. 若錯誤涉及 Kafka broker，先看 Redpanda logs，不要只看 gateway 或 order-service logs

## 6. 成功驗證結果

本輪修復完成後，staging 環境已通過以下檢查：

| 驗證項目 | 結果 |
|----------|------|
| `make staging-health` | 成功回傳 `status=ok` |
| `make staging-smoke-test` | 12/12 checks 通過，`http_req_failed=0.00%` |
| ALB 對外入口 | `BASE_URL=http://<alb-dns>/api/v1` 可正常服務 |
| 核心交易流程 | join、查餘額、查 orderbook、下單、查單、取消單 全數打通 |

## 7. 後續待辦

| 優先級 | 項目 | 原因 |
|--------|------|------|
| High | 導入正式 migration 流程 | 避免每次新環境都需手動匯入 `schema.sql` |
| High | 將現有文件中仍提到 Redpanda + EFS 的內容同步更新 | 避免下次依照舊文件重踩同一個坑 |
| Medium | 補齊 ECS / CloudWatch 告警門檻 | 讓 `UNHEALTHY`、broker 啟動失敗更早被發現 |
| Medium | 補一個 one-off DB bootstrap 指令 | 降低人工透過 CloudShell 匯入的操作成本 |
