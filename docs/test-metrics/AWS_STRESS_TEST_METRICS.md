# AWS 壓力測試關鍵指標

> 本文件整理上 AWS 後壓力測試需要關注的核心指標、對應工具、以及常見瓶頸的解決方案。
> 配合 `ECS_LOADTEST_GUIDE.md` 使用。

---

## 1. 核心監控指標

### 1.1 基礎設施層

| 指標         | 工具             | 閾值                  | 說明                                                |
| ------------ | ---------------- | --------------------- | --------------------------------------------------- |
| CPU 使用率   | CloudWatch       | > 70% 持續 → 考慮擴展 | ECS Task 級別監控                                   |
| 記憶體使用率 | CloudWatch Agent | > 75% → 危險          | EC2 預設**不帶**此指標，需手動安裝 CloudWatch Agent |

> [!WARNING]
> EC2 預設只有 CPU 指標，**記憶體需額外設定 CloudWatch Agent** 才能取得。

---

### 1.2 應用層

| 指標                         | 工具                | 閾值        | 說明                               |
| ---------------------------- | ------------------- | ----------- | ---------------------------------- |
| 請求延遲 P50 / P95 / **P99** | ALB Metrics / X-Ray | P99 < 2s    | **平均值會掩蓋尾部延遲，必看 P99** |
| 錯誤率（4xx）                | ALB Metrics         | < 1%        | 用戶端錯誤（參數錯、404 等）       |
| 錯誤率（5xx）                | ALB Metrics         | < 0.1%      | 服務端錯誤（系統崩潰）             |
| RPS（每秒請求數）            | ALB Metrics         | 依系統目標  | 確認流量有效打進來                 |
| WebSocket 連線數             | 自定義 Metrics      | 依 EC2 規格 | EC2/ALB 有同時連線上限，需特別監控 |

---

### 1.3 資料庫層

| 指標              | 工具                     | 閾值                          | 說明                   |
| ----------------- | ------------------------ | ----------------------------- | ---------------------- |
| DB 連線數         | RDS CloudWatch           | 接近 `max_connections` 的 80% | **通常是第一個爆的點** |
| 查詢延遲 / 慢查詢 | RDS Performance Insights | -                             | 定位具體慢 SQL         |
| Read/Write IOPS   | CloudWatch               | 接近 EBS burst 上限           | 避免 I/O 被限流        |
| DB CPU            | RDS CloudWatch           | > 80%                         | 考慮讀寫分離或加快取   |

---

### 1.4 訊息 / 非同步層（引入 Kafka 後）

| 指標            | 工具               | 說明                       |
| --------------- | ------------------ | -------------------------- |
| 佇列深度        | Kafka Consumer Lag | 持續增長 = consumer 跟不上 |
| 消費延遲        | 自定義 Metrics     | 訊息從入隊到被處理的時間   |
| Producer 錯誤率 | Kafka Metrics      | 寫入失敗的比例             |

---

### 1.5 追蹤 / 瓶頸定位

| 指標                     | 工具              | 說明                                |
| ------------------------ | ----------------- | ----------------------------------- |
| 分散式追蹤               | AWS X-Ray         | 找出哪個服務 / DB 查詢最慢          |
| ALB Target Response Time | ALB Metrics       | 比 EC2 內部指標更能反映用戶實際感受 |
| Auto Scaling 觸發記錄    | CloudWatch Events | 確認 scale-out 有在正確時間點發生   |

---

## 2. 壓測結果 → 決策流程

```
壓測結果分析
│
├── DB CPU > 80% 且讀取 QPS 佔多數
│   └── → 引入 Redis（Cache-Aside）
│
├── DB 連線數接近上限（"too many clients"）
│   └── → 調整連線池參數 或 引入 RDS Proxy
│
├── 單台 CPU > 70% 持續，記憶體 > 75%
│   └── → 水平擴展（ECS Task 多副本 + ALB）
│
├── 下單延遲高但查詢正常
│   └── → 引入 Kafka 非同步削峰
│
├── P99 延遲高但 CPU/DB 正常
│   └── → 檢查 GC、網路延遲、或地理距離（考慮 CloudFront）
│
└── WebSocket 斷線頻繁（多實例後）
    └── → Redis Pub/Sub 做跨實例廣播
```

---

## 3. 常見問題 → 解決方案對照表

| 問題                       | 解決方案              | AWS 工具                    | 適用條件                    |
| -------------------------- | --------------------- | --------------------------- | --------------------------- |
| 高資料庫讀取負載           | 快取（Caching）       | ElastiCache (Redis)         | 讀取佔比 > 70%，重複查詢多  |
| 元件緊密耦合               | 非同步佇列            | SQS / MSK (Kafka)           | 服務間直接呼叫導致連鎖故障  |
| 流量尖峰                   | 水平擴展              | ECS Auto Scaling + ALB      | CPU/記憶體在尖峰時段飽和    |
| API 回應緩慢（可快取內容） | CDN 快取              | CloudFront                  | 靜態資源或可快取的 API 回應 |
| API 回應緩慢（DB 查詢慢）  | 索引優化 + 慢查詢分析 | RDS Performance Insights    | 特定查詢延遲高              |
| DB 連線數打滿              | 連線池代理            | RDS Proxy                   | 高併發短連線場景            |
| WebSocket 跨實例斷線       | Pub/Sub 廣播          | ElastiCache (Redis Pub/Sub) | 多實例 + WebSocket          |
| 日誌分散難追蹤             | 集中日誌              | CloudWatch Logs + X-Ray     | 微服務拆分後                |

> [!IMPORTANT]
> **CloudFront 不是萬能的**。它解決的是「地理距離延遲」或「重複請求快取」，不是所有 API 慢的場景。真正的 API 慢需要先確認根因（DB 慢查詢？運算密集？跨區延遲？），再對症選工具。

---

## 4. k6 壓測關鍵輸出解讀

### 結果指標對照

| k6 指標                    | 含義                       | 良好範圍   |
| -------------------------- | -------------------------- | ---------- |
| `http_req_duration (P95)`  | 95% 請求的完成時間         | < 500ms    |
| `http_req_duration (P99)`  | 99% 請求的完成時間         | < 2000ms   |
| `http_req_failed`          | 請求失敗率                 | < 0.5%     |
| `http_reqs` (rate)         | 每秒請求數 (TPS/RPS)       | 依目標定義 |
| `iteration_duration (avg)` | 每次虛擬使用者迭代平均時間 | < 500ms    |

### 壓測腳本建議配置

```javascript
// 漸進式壓測（推薦）
export const options = {
  stages: [
    { duration: "30s", target: 10 }, // 暖身
    { duration: "1m", target: 50 }, // 基準線
    { duration: "30s", target: 100 }, // 施壓
    { duration: "2m", target: 100 }, // 持續觀察
    { duration: "30s", target: 0 }, // 冷卻
  ],
  thresholds: {
    http_req_duration: ["p(95)<500", "p(99)<2000"],
    http_req_failed: ["rate<0.01"],
  },
};
```

---

## 5. 各階段需關注的指標優先級

| 階段                | 最重要的指標                                 |
| ------------------- | -------------------------------------------- |
| Phase 2（單體壓測） | DB 連線數、P95 延遲、錯誤率                  |
| Phase 3（加 Redis） | Cache Hit Rate、DB CPU 降幅、P95 改善        |
| Phase 4（水平擴展） | ALB 健康檢查、流量分配均勻性、WebSocket 連線 |
| Phase 5（加 Kafka） | Consumer Lag、端到端延遲、訊息遺失率         |
| Phase 6（可觀測性） | 全部指標的 Dashboard 覆蓋率                  |
