# 07 — Production-Ready Backlog

本文件整理 staging 驗證完成後，進入 production-ready 前必須收斂的 backlog。重點不是把所有工程一次做完，而是把 blocking 項目、證據需求與執行順序講清楚。

## 前提

本 backlog 以目前架構假設為基礎：

- `gateway` 為唯一對外入口。
- `order-service`、`matching-engine`、`market-data-service` 仍以首輪單副本穩定性為優先。
- Redpanda staging 仍為單節點。
- 正式 go / no-go 需建立在 staging baseline 與 correctness audit 結果之上。

## P0：Production 前阻塞項目

| 項目 | 風險 | 需要的證據 | 建議交付物 |
|------|------|------------|------------|
| 告警與 SLO baseline | 無法在異常時即時發現 | CPU、memory、5xx、ALB latency、consumer lag 告警策略 | CloudWatch Alarm / Grafana 告警清單 |
| 正式 rollback runbook | 發版失敗時恢復過慢 | 至少一次受控 rollback 演練 | rollback runbook + 演練紀錄 |
| Correctness audit 固化 | 壓測通過但交易資料錯誤 | 自動化或半自動 audit 流程 | SQL / script 與報告模板 |
| matching-engine failover 策略 | leader 或 task 故障會中斷撮合 | 重啟與恢復時間量測 | failover drill 結果 |
| market-data graceful drain | WS 連線可能在 deploy 時大量斷線 | reconnect 行為與使用者體驗證據 | drain / reconnect runbook |
| Redpanda HA 決策 | 單節點 broker 是明確 SPOF | 是否保留 self-managed 或改 managed Kafka 的決策依據 | 評估報告 |

## P1：下一輪強化項目

| 項目 | 目的 | 備註 |
|------|------|------|
| gateway autoscaling policy | 讓入口層可隨流量擴縮 | 需先有 baseline 指標 |
| order-service 多副本策略 | 釐清 outbox、consumer、settlement 在多副本下的正確性 | 先做模型驗證，再擴副本 |
| market-data stickiness / drain | 降低 WS 客戶端 deploy 期間中斷 | 可搭配 ALB stickiness 或 app-level reconnect |
| soak test 標準流程 | 找慢性記憶體 / goroutine / lag 累積問題 | 建議 30 分鐘、4 小時、24 小時三層 |
| DB / Redis 觀測補齊 | 強化 bottleneck 定位能力 | 補 pg_stat_statements、Redis 指標圖表 |

## P2：中期架構選項

| 項目 | 問題意識 | 評估方向 |
|------|----------|----------|
| Managed Kafka / MSK / Confluent | 降低自維運 Redpanda 風險 | 成本、SLA、遷移複雜度 |
| matching-engine standby 模式 | 單 active task 的可用性不足 | active-standby + fencing token |
| 多 AZ / fault isolation | 單 NAT / 單 broker / 單副本不適合 production | 逐層拆分單點 |
| 自動化 deploy pipeline | 手動 deploy 難以追蹤與審計 | GitHub Actions / Argo / 專用 CD |

## Go / No-Go Gate

### Go 前至少需要成立

- [ ] staging baseline 與 correctness audit 連續通過。
- [ ] 有明確 rollback runbook，且至少演練一次。
- [ ] 有基本告警與 on-call 可觀測面。
- [ ] 有 matching-engine 重啟恢復時間與故障處置手冊。
- [ ] 有 WebSocket deploy 期間的客戶端體驗策略。

### 若下列任一項仍未知，建議維持 No-Go

- [ ] 不知道 broker 故障時的恢復時間。
- [ ] 不知道 order-service 多副本是否會破壞 outbox / settlement 行為。
- [ ] 不知道 market-data deploy 時會掉多少 WS 連線。
- [ ] 不知道 correctness audit 在壓力下是否穩定通過。

## 建議執行順序

1. 先把 staging 驗證報告補齊，包含 correctness audit 與 controlled restart。
2. 先做告警與 rollback，再談 autoscaling。
3. 先處理 matching-engine / market-data 的可恢復性，再推進多副本。
4. 最後再做 managed service 與長期架構選型評估。

## 交付物對照

| 類型 | 建議內容 |
|------|----------|
| Runbook | rollback、failover、WS deploy、incident triage |
| 圖表 / 告警 | ECS、ALB、RDS、Redis、Kafka 核心指標與告警閾值 |
| 報告 | staging baseline、correctness audit、failover drill、service evaluation |
| 決策紀錄 | Redpanda 是否保留、autoscaling 策略、production go / no-go |