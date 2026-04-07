# 06 — Staging 驗證 Runbook

本文件是 ECS staging 部署後的正式驗收 runbook。目標是讓每次驗證都遵循同一個順序，避免臨時拼指令、遺漏 correctness audit 或把 WebSocket 驗證做成無效測試。

## 驗證輸入

| 欄位 | 建議填法 |
|------|----------|
| 日期 | `YYYY-MM-DD` |
| 環境 | `staging` |
| Image Tag | `git rev-parse --short HEAD` |
| 操作者 | 實際執行人 |
| 資料量等級 | `S` / `M` / `L` |
| 驗證範圍 | `gateway + order-service + matching-engine + market-data-service` |

## Phase 0：Preflight

### 必做檢查

- [ ] `aws sts get-caller-identity` 成功，且帳號正確。
- [ ] `make show-staging-outputs` 可列出 `ALB_DNS`、`BASE_URL`、`WS_URL`。
- [ ] `make ecs-status-all` 中四個核心服務皆可被查詢。
- [ ] `docs/testing/TEST_REPORT_TEMPLATE.md` 已複製成當輪結果文件。

### 建議指令

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend

aws sts get-caller-identity
make show-staging-outputs
make ecs-status-all
```

## Phase 1：Steady State Gate

### 目標

確認服務不是「剛建立成功」而已，而是真的進入穩定狀態。

### 檢查項目

- [ ] `make ecs-status-all` 中每個 service 無持續失敗的 deployment events。
- [ ] `gateway`、`order-service`、`matching-engine`、`market-data-service` 無重複 crash loop。
- [ ] CloudWatch logs 沒有連續性 panic、credentials missing、DNS resolve 失敗。

### 建議指令

```bash
make ecs-status-all
make ecs-logs ECS_SERVICE=gateway
make ecs-logs ECS_SERVICE=order-service
```

## Phase 2：Gateway / ALB Health Gate

### 檢查項目

- [ ] `make staging-health` 回傳 `200`。
- [ ] `/health` 不應依賴尚未就緒的 downstream 而回 5xx。
- [ ] ALB target health 顯示 `gateway` healthy。

### 指令

```bash
make staging-health
```

## Phase 3：Smoke Test

### 目標

確認核心交易流程可打通：join、查餘額、查 orderbook、下單、查單、取消。

### 檢查項目

- [ ] `make staging-smoke-test` 跑完。
- [ ] 無持續性 5xx。
- [ ] 回傳 JSON 結構符合預期。

### 指令

```bash
make staging-smoke-test SYMBOL=BTC-USD
```

## Phase 4：HTTP Load Baseline

### 目標

以固定 baseline 驗證 P95、錯誤率與基本吞吐。

### 檢查項目

- [ ] `make staging-load-test` 跑完。
- [ ] `http_req_failed` 沒有持續超標。
- [ ] P95 沒有在低負載就持續上升。
- [ ] ECS CPU / memory 沒有立即打滿。

### 指令

```bash
make staging-load-test
```

### 觀測建議

| 指標 | 觀察位置 |
|------|----------|
| P50 / P95 / P99 | k6 terminal 輸出 |
| Gateway CPU / Memory | ECS Service metrics |
| RDS active connections | `pg_stat_activity` |
| 5xx 與 deployment events | CloudWatch logs + ECS events |

## Phase 5：WebSocket Fanout 驗證

### 重要前提

WebSocket fanout 驗證必須與持續打單流量並行執行。若沒有訂單事件流，僅建立 WS 連線無法有效驗證 broadcast 路徑。

### 執行方式

終端 A：

```bash
make staging-load-test K6_ENV_FLAGS="--vus 100 --duration 2m"
```

終端 B：

```bash
make staging-ws-validation
```

### 檢查項目

- [ ] WebSocket 連線建立成功率接近 100%。
- [ ] 連線期間能持續收到訊息。
- [ ] market-data-service logs 沒有大量斷線或 broadcast error。

## Phase 6：Correctness Audit

### 目標

壓測結束後，不只要看 latency，也要確認交易所核心不變量沒有被破壞。

### 檢查項目

- [ ] 資產守恆通過。
- [ ] `balance + locked` 一致。
- [ ] 無 stuck `NEW` / `PARTIALLY_FILLED` orders。
- [ ] `FILLED` order 與 trades 對帳一致。

### SQL 與紀錄位置

請直接使用 `docs/testing/TEST_REPORT_TEMPLATE.md` 中的 SQL 區段，將結果貼進本輪報告。

## Phase 7：Controlled Restart

### 目標

確認單一服務受控重啟後，系統能恢復而不是留下不穩定狀態。

### 建議順序

1. `gateway`
2. `market-data-service`
3. `matching-engine`

### 建議做法

```bash
IMAGE_TAG=<目前正在跑的 tag>

make ecs-deploy ECS_SERVICE=gateway IMAGE_TAG=$IMAGE_TAG
make staging-health

make ecs-deploy ECS_SERVICE=market-data-service IMAGE_TAG=$IMAGE_TAG
make ecs-status ECS_SERVICE=market-data-service
```

### 檢查項目

- [ ] `gateway` 重啟後 `/health` 可恢復。
- [ ] `market-data-service` 重啟後 WebSocket 可重新連上。
- [ ] `matching-engine` 重啟後 leader election 與 snapshot restore 正常。

## 驗收出口條件

以下項目全部成立，才可視為本輪 staging 驗證通過：

- [ ] 四個核心服務維持 steady state。
- [ ] Health、smoke、load、WebSocket 驗證皆通過。
- [ ] Correctness audit 無異常。
- [ ] Controlled restart 無持續性錯誤。
- [ ] 測試結果已落檔到 `docs/testing/TEST_REPORT_TEMPLATE.md` 對應報告。

## 失敗時的最小處置

| 問題 | 第一個動作 | 第二個動作 |
|------|------------|------------|
| `gateway /health` 失敗 | `make ecs-status ECS_SERVICE=gateway` | `make ecs-logs ECS_SERVICE=gateway` |
| Smoke 出現 5xx | 檢查 `gateway` 與 `order-service` logs | 確認 downstream DNS / SSM 注入 |
| Load 時 latency 異常飆升 | 記錄 P95 與當下 CPU / memory | 暫停測試，先做 bottleneck 定位 |
| WebSocket 無法收到訊息 | 確認是否有並行打單流量 | 檢查 `market-data-service` 與 Kafka 消費 |
| Correctness audit 失敗 | 直接判定本輪失敗 | 先修 correctness，再做下一輪壓測 |