# Grafana 監控指標說明

本目錄包含 Exchange Load Testing Dashboard（`exchange-load-testing-dashboard.json`）的設定檔，可直接匯入 Grafana。

---

## 匯入方式

1. 開啟 Grafana → Dashboards → Import
2. 上傳 `exchange-load-testing-dashboard.json`
3. 確認 Prometheus datasource 指向正確目標

---

## 已具備的指標面板

Dashboard 共 14 個 panel，分為 5 個觀測層。

### HTTP 層（4 個 panel）

| Panel | Prometheus 指標 | 用途 | 看什麼 |
| :--- | :--- | :--- | :--- |
| HTTP RPS | `exchange_http_requests_total` | 每秒請求數 | 確認流量有打進去；spike 時看峰值 |
| HTTP 5xx Error Rate | `exchange_http_requests_total{status=~"5.."}` | 伺服器錯誤率 | 壓測中應 < 0.1%；spike 後應恢復 |
| HTTP P95 Latency | `exchange_http_request_duration_seconds_bucket` | 95 百分位延遲 | 負載增加時注意是否線性惡化或出現拐點 |
| HTTP P95 by Service | 同上 by `service` | 服務層別 P95 | 找出是哪個服務在惡化 |

額外 timeseries：
- **HTTP Throughput by Service**：哪個服務承擔最多流量
- **HTTP Status Distribution**：200 / 4xx / 5xx 比例

### 訂單層（2 個 panel + timeseries）

| Panel | Prometheus 指標 | 用途 | 看什麼 |
| :--- | :--- | :--- | :--- |
| Order Throughput | `exchange_orders_total` | 每秒下單數 by mode/side/type | 確認 order 有在累積；async mode 應比 sync 更高 |
| Order Processing P95 | `exchange_order_processing_duration_seconds_bucket` | 訂單處理時間 | > 500ms 需關注；soak 時應穩定不漂移 |

### 交易層

| Panel | Prometheus 指標 | 用途 | 看什麼 |
| :--- | :--- | :--- | :--- |
| Trades Executed | `exchange_trades_executed_total` | 每秒成交筆數 | 與 order throughput 比對；大量買賣應有成交 |

### Kafka 事件層（2 個 panel）

| Panel | Prometheus 指標 | 用途 | 看什麼 |
| :--- | :--- | :--- | :--- |
| Kafka Event Throughput | `exchange_kafka_events_total` | Kafka 事件速率 by component/handler | 確認 consumer 有在消費；event 量應與 order 量對應 |
| Kafka Event P95 | `exchange_kafka_event_duration_seconds_bucket` | Kafka 事件處理時間 | > 200ms 需關注；soak 時應穩定 |

### WebSocket 層（3 個 panel）

| Panel | Prometheus 指標 | 用途 | 看什麼 |
| :--- | :--- | :--- | :--- |
| WebSocket Connections | `exchange_websocket_connections` | 同時連線數 | ws-fanout 時應能撐到 2000 以上 |
| WebSocket Broadcast Throughput | `exchange_websocket_broadcast_total` | 每秒廣播次數 by result | `result="success"` 應主導 |
| WebSocket Broadcast Loss Rate | 同上 by `result=~"dropped\|client_buffer_full"` | 廣播丟棄率 | 目標 < 1%；高於 5% 需調整 buffer 或架構 |

---

## 尚未具備的指標（缺口）

以下指標完全沒有 Prometheus 採集或 Grafana panel，但對交易所級壓測是必要的。

### PostgreSQL 層（高優先）

| 指標 | 重要性 | 為何重要 |
| :--- | :--- | :--- |
| `pg_stat_activity` active connections | 高 | DB 連線池滿是常見 bottleneck |
| Lock wait count | 高 | 高並發下鎖競爭是金額錯誤的根源 |
| Slow query (mean_exec_time) | 高 | 大資料量下 query plan 退化不會主動報警 |
| Table / index size | 中 | 資料量成長時需確認索引未 bloat |
| `pg_stat_bgwriter` checkpoint / write | 中 | soak test 時觀察 I/O 是否飽和 |

暫時替代方案：壓測期間手動執行 `TEST_REPORT_TEMPLATE.md` 內的 SQL 查詢。

### Redis 層（高優先）

| 指標 | 重要性 | 為何重要 |
| :--- | :--- | :--- |
| Used memory / max memory | 高 | 達上限後 eviction 會造成邏輯錯誤 |
| Eviction count | 高 | 有 eviction 表示 cache 已飽和 |
| Cache hit rate | 中 | 低命中代表 orderbook snapshot 重建頻繁 |
| Connected clients | 低 | 輔助觀察連線數是否正常 |

### Kafka 消費者層（高優先）

| 指標 | 重要性 | 為何重要 |
| :--- | :--- | :--- |
| Consumer lag per topic / group | 高 | lag 飆升代表 consumer 跟不上，訂單事件積壓 |
| Publish failure count | 高 | 發送失敗會導致訂單流中斷 |
| Retry count per handler | 中 | 高重試代表下游不穩定 |

### Go Runtime 層（soak test 必要）

| 指標 | 重要性 | 為何重要 |
| :--- | :--- | :--- |
| Goroutine count | 高 | 緩慢增長是 goroutine leak 的早期訊號 |
| Heap alloc / heap in-use | 高 | soak 時 heap 應穩定；線性成長代表洩漏 |
| GC pause duration | 中 | GC 頻繁時會影響 P99 latency |

Go runtime metrics 可透過 `prometheus/client_golang` 的 `collectors.NewGoCollector()` 自動採集。

### Correctness 層（交易所特有）

| 指標 | 重要性 | 為何重要 |
| :--- | :--- | :--- |
| stuck order count | 高 | 代表撮合或結算出現異常 |
| balance integrity check | 高 | balance + locked 不一致是金額 bug 的直接表現 |
| locked fund mismatch | 高 | 有 locked 但無對應 open order 是嚴重問題 |

這些無法單靠 Prometheus 採集，需定期執行 SQL 查詢（見 `TEST_REPORT_TEMPLATE.md`）或開發 exporter。

---

## 各測試類型應重點觀察的 Panel

| 測試類型 | 主要觀察 | 次要觀察 |
| :--- | :--- | :--- |
| smoke-test | HTTP 5xx Error Rate、HTTP RPS | Order Throughput |
| load-test | HTTP P95 Latency、Order Throughput、Kafka Event P95 | HTTP Status Distribution |
| spike-test | HTTP 5xx Error Rate 尖峰後恢復曲線、HTTP P95 | WS Connections |
| ws-fanout-test | WebSocket Connections、Broadcast Loss Rate | Broadcast Throughput |
| 容量測試 | HTTP P95 Latency 拐點、Order Processing P95 | Kafka Event Throughput |
| soak test | Order Processing P95 長時間趨勢、Kafka Event P95、WS Broadcast Loss | 若有 Go runtime 指標：Goroutine count、Heap |

---

## 相關文件

- 壓測執行步驟：`../TEST_EXECUTION_RUNBOOK.md`
- 測試結果報告模板：`../TEST_REPORT_TEMPLATE.md`
- 線上 ECS 測試文件：`../ECS_TESTING.md`
