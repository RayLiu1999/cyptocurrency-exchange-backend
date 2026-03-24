# 線上 ECS 測試文件

本文件只回答 staging / ECS / 接近生產環境的測試問題：

1. 線上要怎麼分階段壓測。
2. 哪些結論一定要在線上驗證。
3. 要看哪些 metrics、correctness audit 與資料量指標。
4. staging / ECS 的容量、韌性與 soak test 怎麼安排。

## 0. 總目標

線上 ECS 測試的總目標，是取得可以支撐架構決策的證據，而不是只得到一份「壓測跑過了」的紀錄。

線上階段應達成：

1. 確認真實容量邊界與 tail latency 拐點。
2. 確認大資料量下瓶頸是否改變。
3. 確認高流量下金額正確性沒有被破壞。
4. 確認依賴層失效後是否能恢復。
5. 確認長時間運行不會出現慢性退化。

## 0.1 目前已具備與尚未具備

### 已具備

| 項目 | 現況 |
| :--- | :--- |
| ECS 部署能力 | 已具備 `ecs-deploy`、`ecs-status` 等基礎部署指令 |
| 可手動將 k6 打到線上 | 已具備，可透過 `BASE_URL` 指向 ALB / gateway |
| 線上觀測方向 | 文件已定義效能、correctness、資料量、依賴層觀測項目 |

### 尚未具備

| 項目 | 缺口 |
| :--- | :--- |
| correctness audit 自動化 | 尚無壓測後自動對帳、資產守恆、stuck order 稽核 |
| 資料量分層壓測落地 | 尚無 S / M / L / XL 資料集生成與固定執行流程 |
| 韌性 / chaos 腳本 | 尚無 Kafka / Redis / PostgreSQL 故障注入工具 |
| soak test 自動化 | 尚無長時間壓測包裝、監控彙整與驗收門檻 |
| ECS 壓測 orchestration | 尚無把 baseline、資料量分層、audit、韌性、soak 串成一套流程 |

## 1. 線上測試的角色

線上測試的目標是取得可用於架構決策的證據，而不是做第一輪除錯。

線上主要驗證：

1. 真實容量邊界。
2. 大資料量行為。
3. 多實例與真實網路路徑。
4. 故障恢復能力。
5. 長時間穩定性。

## 2. 線上適合做的測試

| 類型 | 適合原因 | 代表場景 |
| :--- | :--- | :--- |
| ECS baseline | 能反映 ALB、ECS、RDS、Redis、Kafka 的真實成本 | smoke / load on ECS |
| 單一 symbol 容量測試 | 可找到真實 tail latency 拐點與瓶頸層 | 單一交易對壓測 |
| 多實例壓測 | 可觀察 ALB、Auto Scaling、WS 跨實例表現 | ECS 多副本場景 |
| 大資料量壓測 | 更容易暴露 DB、索引、snapshot restore 與歷史查詢問題 | M / L / XL 級資料量 |
| correctness audit | 壓測後做資產守恆與 stuck order 稽核 | 壓測後對帳 |
| 韌性測試 | 可驗證依賴失效與恢復時間 | Kafka / Redis / DB chaos |
| soak test | 可觀察記憶體、goroutine、lag 緩慢累積 | 30m / 24h load |

## 3. 線上不適合優先做的事情

| 類型 | 原因 |
| :--- | :--- |
| 腳本第一輪除錯 | 成本高且結果噪音多 |
| 邏輯正確性的第一輪驗證 | 這應該在本地先收斂 |
| 尚未穩定的 baseline 測試 | 本地未穩定時，線上結果無法解讀 |

## 4. staging / ECS 壓測執行順序

以下階段分成兩類：

1. 現在已可執行的階段。
2. 文件要求但尚未補齊自動化的階段。

| 階段 | 目的 | 重點輸出 |
| :--- | :--- | :--- |
| Phase 0 | 環境健康檢查 | smoke、health、metrics 可用 |
| Phase 1 | ECS baseline | P50 / P95 / P99、error rate、CPU / memory |
| Phase 2 | 單一熱門交易對容量測試 | 穩定 TPS / RPS、第一個瓶頸 |
| Phase 3 | 資料量分層測試 | S / M / L 資料量下的退化差異 |
| Phase 4 | correctness audit | 資產守恆、stuck order、locked funds |
| Phase 5 | 韌性測試 | Kafka / Redis / DB 故障恢復 |
| Phase 6 | soak test | 記憶體、goroutine、lag、DB latency 長時間趨勢 |

### 4.1 階段落地現況

| 階段 | 目前狀態 |
| :--- | :--- |
| Phase 0 | 已可執行 |
| Phase 1 | 已可執行 |
| Phase 2 | 可手動執行，但尚無標準化報告輸出 |
| Phase 3 | 文件已定義，尚未具備資料生成器與固定資料集 |
| Phase 4 | 文件已定義，尚未具備自動 audit |
| Phase 5 | 文件已定義，尚未具備故障注入腳本 |
| Phase 6 | 可手動延長 k6 執行，但尚未形成完整 soak 流程 |

## 5. 線上資料量分層

| 級別 | 建議資料量 | 是否適合 ECS / staging | 主要觀察點 |
| :--- | :--- | :--- | :--- |
| S | `10^4` | ✅ 可做 baseline | 環境健康與腳本正確 |
| M | `10^6` | ✅ 強烈建議 | 查詢延遲、索引效率、快照大小 |
| L | `10^7` | ✅ 必要 | 歷史查詢、恢復時間、cache miss、vacuum / bloat |
| XL | `10^8+` | ✅ 僅限專用環境 | 真實成長風險與長期容量邊界 |

如果只在 S 級資料量做 ECS 壓測，報告只能標註為「小資料集容量基線」。

## 6. 線上觀測重點

### 6.1 效能

- P50 / P95 / P99
- error rate
- RPS / TPS
- in-flight requests
- ALB target response time

### 6.2 correctness audit

- 資產總量守恆
- `balance + locked` 一致
- stuck `NEW` / `PARTIALLY_FILLED` order
- trade / order 對帳一致

### 6.3 資料量層

- `orders` / `trades` / `accounts` 總量
- 活躍 symbol 數
- 活躍 orderbook depth
- 表與索引大小
- snapshot rebuild 時間

### 6.4 依賴層

- PostgreSQL active connections
- lock wait / slow query
- Kafka consumer lag / publish fail / retry count
- Redis 降級與快取失效率
- WebSocket broadcast latency / dropped client count

## 7. 線上驗收原則

線上壓測至少要同時回答：

1. 高流量下金額有沒有錯。
2. 哪一層先成為瓶頸。
3. 資料量變大後，瓶頸有沒有改變。
4. 依賴出問題後能不能恢復。
5. 長時間運行是否出現慢性退化。

如果目前只能回答第 2 項的一部分，卻無法回答第 1、4、5 項，那就代表線上測試還停留在「負載測試」，尚未升級成「交易所級驗收測試」。

## 8. 線上測試結論的使用方式

只有線上或接近生產的測試結果，才適合拿來做以下決策：

1. 是否需要調整 ECS task size。
2. 是否需要優化 RDS / Redis / Kafka 配置。
3. 是否需要做 query / index 優化。
4. 是否需要拆分服務或調整架構。