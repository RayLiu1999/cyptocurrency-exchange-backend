# 04 — ECS 微服務部署執行清單

本文件將目前的 AWS ECS 重整工作拆成可直接執行的 issue/checklist。
目標不是先把 deploy 文件寫得漂亮，而是先建立一條可以在 staging 跑通、可重複驗證、並能自然延伸到 production-ready 的微服務部署路徑。

## 目前進度

- [x] **ECS-01**：已新增現行微服務部署契約文件，凍結服務拓樸、環境變數與內部 DNS 規則。
- [x] **ECS-02**：已在 Makefile、deploy 入口與 legacy monolith 說明中標記並隔離舊部署假設。
- [x] **ECS-03**：已建立 Terraform remote state bootstrap 目錄與操作入口。
- [x] **ECS-04**：已補齊 staging outputs、Cloud Map service registry、ALB / SG 端口與 SSM / secret mapping 介面。
- [x] **ECS-05**：已建立 gateway 與 order-service 的 ecspresso task / service definitions，部署入口改用 Terraform outputs 注入。
- [x] **ECS-06**：已建立 matching-engine 與 market-data-service 的 ecspresso task / service definitions，補齊 leader election / WebSocket 依賴設定。
- [x] **ECS-07**：已重整 Makefile 與 deploy 文件入口，補上 per-service create / deploy / status / rollout / validation 指令。
- [x] **ECS-08**：已建立 staging 驗證 runbook，串起 health、smoke、load、WebSocket、correctness audit 與 controlled restart。
- [-] **ECS-09**：已補齊 rollout / baseline 指令與驗收路徑；待以真實 AWS staging 環境執行 deploy 與驗證。
- [x] **ECS-10**：已建立 production-ready backlog 與 go / no-go gate。

## 目標與邊界

### 本輪目標

| 項目 | 說明 |
|------|------|
| **主要目標** | 以現行 microservice runtime 為基準，完成 staging ECS 部署與基本穩定性驗證 |
| **驗證目標** | health check、smoke test、baseline load、WebSocket 路徑、correctness audit |
| **部署邊界** | 沿用現有技術棧：ECS + RDS PostgreSQL + ElastiCache Redis + Redpanda |
| **文件策略** | 先補齊可執行 checklist，再逐步淘汰 legacy monolith 文件 |

### 本輪範圍

| 類型 | 納入範圍 | 暫不列為 blocking scope |
|------|----------|--------------------------|
| **應用服務** | gateway、order-service、matching-engine、market-data-service | simulation-service |
| **雲端資源** | VPC、ALB、ECS、RDS、Redis、Redpanda、SSM、CloudWatch | 完整 CI/CD、自動化 rollback pipeline |
| **測試驗證** | smoke、load、ws-fanout、手動 correctness audit | 24 小時 soak、完整 chaos engineering |
| **高可用強化** | production-ready backlog 定義 | 第一輪就完成 autoscaling 與多活 |

## 現行服務拓樸

| 服務 | 主要責任 | Port | 對外暴露 | 主要依賴 | 首輪副本策略 |
|------|----------|------|----------|----------|----------------|
| **gateway** | HTTP / WebSocket 單一入口，反向代理 order-service、market-data-service、simulation-service | 8100 | 經 ALB 對外 | order-service、market-data-service、Redis | `1 -> 2` |
| **order-service** | 下單、資金鎖定、Outbox、settlement consumer | 8103 | 否 | PostgreSQL、Kafka、Redis | `1` |
| **matching-engine** | 記憶體撮合、leader election、snapshot restore、行情事件發布 | 8101 | 否 | PostgreSQL、Kafka、Redis | `1` |
| **market-data-service** | WebSocket 推播、查詢 API、行情事件消費 | 8102 | 否 | PostgreSQL、Kafka、Redis | `1` |
| **simulation-service** | 壓測控制面 API | 8104 | 否 | gateway | 非首輪必要 |

## 相關目錄與交付物

```text
deploy/
├── terraform/
│   ├── modules/
│   │   ├── network/
│   │   ├── container/
│   │   ├── data/
│   │   ├── messaging/
│   │   └── alb/
│   ├── bootstrap/
│   └── environments/
│       └── staging/
├── ecspresso/
│   ├── monolith/                  # legacy，僅供拆分參考
│   ├── gateway/                   # 已補 task/service def
│   ├── order-service/             # 已補 task/service def
│   ├── matching-engine/           # 已補 task/service def
│   └── market-data-service/       # 已補 task/service def
└── docs/
    ├── 01-QUICKSTART.md
    ├── 02-DAILY-WORKFLOW.md
    ├── 03-TEARDOWN.md
    ├── 04-ECS-MICROSERVICES-EXECUTION-CHECKLIST.md
    ├── 05-CURRENT-SERVICE-CONTRACT.md
    ├── 06-STAGING-VALIDATION-RUNBOOK.md
    └── 07-PRODUCTION-READY-BACKLOG.md
```

## Issue 拆分

| ID | 任務 | 我會做什麼 | 主要交付物 | 依賴 | 完成定義 |
|----|------|------------|------------|------|----------|
| **ECS-01** | 凍結現行微服務拓樸 | 以 `cmd/*/main.go` 確認對外入口、服務依賴、環境變數契約與流量路徑 | `deploy/docs/05-CURRENT-SERVICE-CONTRACT.md` | 無 | gateway / order / matching / market-data 的 port、upstream、secret 名稱與部署角色被明確列出 |
| **ECS-02** | 標記並隔離 legacy monolith 假設 | 清出 monolith-only 文件、Makefile 指令與 ecspresso 入口，避免誤用 | Makefile legacy guard、deploy 入口標記 | ECS-01 | 開發者不會再把 monolith quickstart 當成現行部署路徑 |
| **ECS-03** | 建立 Terraform remote state bootstrap | 補上 S3 + DynamoDB bootstrap 與啟用步驟，讓 staging state 可重複管理 | `deploy/terraform/bootstrap/` | ECS-01 | staging 可以使用 remote state init / plan / apply |
| **ECS-04** | 對齊 staging infra outputs 與 SSM / secrets mapping | 檢查 Terraform outputs 是否足夠支援 per-service deployment，整理 env 與 secret 命名 | output / secret 對照表 | ECS-03 | 每個服務的 task definition 都能引用正確 infra output 與 secret |
| **ECS-05** | 建立 gateway 與 order-service ECS 定義 | 建 task/service definitions、health check、log group、image tag、network 與 env | `deploy/ecspresso/gateway/`、`deploy/ecspresso/order-service/` | ECS-04 | gateway 與 order-service 可由 ecspresso 建立或部署 |
| **ECS-06** | 建立 matching-engine 與 market-data-service ECS 定義 | 補 task/service definitions，對齊 leader election、Kafka、WebSocket 與 Redis 需求 | `deploy/ecspresso/matching-engine/`、`deploy/ecspresso/market-data-service/` | ECS-04 | matching-engine 與 market-data-service 可在 ECS steady state 啟動 |
| **ECS-07** | 重整 Makefile 與部署入口 | 將 deploy 指令從 monolith 改為微服務模式，移除 `cmd/server`、`internal/core` 歷史殘留 | `Makefile`、`deploy/README.md`、`deploy/docs/01-03.md` | ECS-05、ECS-06 | `make` 指令能對應現行微服務部署與驗證流程 |
| **ECS-08** | 建立 staging 驗證清單 | 將 health、ALB、logs、k6、correctness audit 整理成可勾選的執行順序 | `deploy/docs/06-STAGING-VALIDATION-RUNBOOK.md` | ECS-07 | 部署後可依 checklist 完成驗證，不需臨時拼湊指令 |
| **ECS-09** | 執行 staging 部署驗證 | 套 infra、push image、部署服務、驗證 ALB 路徑與 k6 baseline | 測試記錄、問題清單、修正項 | ECS-08 | 四個核心服務上線並通過首輪 baseline 驗收 |
| **ECS-10** | 收斂 production-ready backlog | 根據 staging 結果定義 autoscaling、告警、soak、failover drill 與 managed service 評估 | `deploy/docs/07-PRODUCTION-READY-BACKLOG.md` | ECS-09 | 第二輪強化任務有明確優先序與風險說明 |

## 建議 GitHub Issue 標題

1. `ECS-01 凍結現行微服務拓樸與部署契約`
2. `ECS-02 標記並隔離 legacy monolith deploy 入口`
3. `ECS-03 建立 Terraform remote state bootstrap`
4. `ECS-04 對齊 staging outputs 與 secrets mapping`
5. `ECS-05 建立 gateway 與 order-service ECS 定義`
6. `ECS-06 建立 matching-engine 與 market-data-service ECS 定義`
7. `ECS-07 重整 Makefile 與部署文件入口`
8. `ECS-08 建立 staging 驗證 runbook 與 checklist`
9. `ECS-09 執行 staging ECS 部署與 baseline 驗證`
10. `ECS-10 收斂 production-ready backlog`

## 執行順序

| 階段 | 內容 | 是否 blocking |
|------|------|----------------|
| **Phase A** | ECS-01、ECS-02 | 是 |
| **Phase B** | ECS-03、ECS-04 | 是 |
| **Phase C** | ECS-05、ECS-06 | 是 |
| **Phase D** | ECS-07、ECS-08 | 是 |
| **Phase E** | ECS-09 | 是 |
| **Phase F** | ECS-10 | 否 |

## 驗收標準

| 項目 | 驗收方式 | 通過條件 |
|------|----------|----------|
| **Terraform state** | `terraform init -reconfigure`、`plan`、`apply` | staging 不再依賴本機 local state |
| **ECS steady state** | `ecspresso status`、ECS console、CloudWatch logs | 四個核心服務都能進入 steady state |
| **ALB / Gateway** | `curl http://$ALB/health`、API 路徑實測 | `/health` 正常，gateway 能轉發 orderbook / order / ws 流量 |
| **Load baseline** | `make smoke-test`、`make load-test`、`make ws-fanout-test` | 無持續性 5xx，錯誤率與延遲落在首輪 baseline 內 |
| **Correctness audit** | `docs/testing/TEST_REPORT_TEMPLATE.md` 的 SQL 檢核 | 資金守恆、無 stuck orders、trade / settlement 狀態一致 |
| **Controlled restart** | 受控重啟單一 task | gateway 可恢復健康，matching-engine 可重新取得 leader，market-data 連線可恢復 |

## 技術風險與目前決策

| 風險 | 影響 | 目前決策 |
|------|------|----------|
| **Redpanda 目前為單節點** | broker 故障會影響整體事件流 | staging 先接受，production-ready 再評估 HA / managed Kafka |
| **matching-engine 為記憶體引擎** | failover 需依賴 snapshot restore 與 leader election | 首輪先單 active task，後續再做 standby / failover drill |
| **market-data-service 維護 WebSocket 連線** | 多副本下需處理 stickiness 或 graceful drain | 首輪先單副本驗證，後續再擴充 |
| **order-service 內含 outbox worker** | 多副本時需確認補發與消費模型 | 首輪先單副本，待 staging 穩定後再評估擴展策略 |
| **KAFKA_RESET_OFFSET 設錯** | 可能重播歷史事件污染現況資料 | staging 與 production 預設維持 `latest` |

## 立即下一步

- [ ] 執行 **ECS-09**：在真實 AWS staging 環境跑 `make ecs-deploy-core`、`make staging-baseline-test` 與 correctness audit。
- [ ] 將結果填入 `docs/testing/TEST_REPORT_TEMPLATE.md`，作為 ECS-09 的正式驗證記錄。

## 參考檔案

- `cmd/gateway/main.go`
- `cmd/order-service/main.go`
- `cmd/matching-engine/main.go`
- `cmd/market-data-service/main.go`
- `deploy/terraform/environments/staging/main.tf`
- `deploy/README.md`
- `deploy/docs/06-STAGING-VALIDATION-RUNBOOK.md`
- `deploy/docs/07-PRODUCTION-READY-BACKLOG.md`
- `docs/testing/ECS_TESTING.md`
- `docs/testing/TEST_EXECUTION_RUNBOOK.md`
- `docs/testing/TEST_REPORT_TEMPLATE.md`