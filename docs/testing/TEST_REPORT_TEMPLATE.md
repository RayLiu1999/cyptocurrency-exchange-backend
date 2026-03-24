# 測試結果報告模板

每輪測試完成後，依此模板填入結果並存檔。存檔命名建議：`results/YYYY-MM-DD_<env>_<test-type>.md`。
文件不用完美，但每個 **判斷依據** 欄位必須填入實際數字，而非「感覺還好」。

---

## 1. 基本資訊

| 欄位 | 填入值 |
| :--- | :--- |
| 日期 | YYYY-MM-DD |
| 環境 | `local` / `staging` / `ECS` |
| Git Commit / Image Tag | |
| 測試類型 | smoke / load / spike / ws-fanout / capacity / soak |
| 資料量等級 | S (`10^4`) / M (`10^6`) / L (`10^7`) / XL (`10^8+`) |
| 資料量背景 | orders: __ / trades: __ / accounts: __ / orderbook depth: __ |
| 資料表總大小 | |
| 執行指令 | `make smoke-test BASE_URL=...` 等完整指令 |

---

## 2. 效能結果

### 2.1 HTTP 層

| 指標 | 結果 | 判斷 |
| :--- | :--- | :--- |
| RPS（穩定期） | | ✅ / ⚠️ / ❌ |
| P50 latency | | |
| P95 latency | | |
| P99 latency | | |
| 5xx Error Rate | | 目標 < 0.1% |
| Max in-flight requests | | |

### 2.2 訂單與交易

| 指標 | 結果 | 判斷 |
| :--- | :--- | :--- |
| Order TPS（穩定期） | | |
| Order Processing P95 | | |
| Trade 成交量（總計） | | |
| 成交率（matched / placed） | | |

### 2.3 Kafka 事件層

| 指標 | 結果 | 判斷 |
| :--- | :--- | :--- |
| Kafka Event Throughput | | |
| Kafka Event P95 | | |
| Consumer Lag（最大值） | 尚無監控 | ⚠️ 待補 |
| Publish 失敗次數 | 尚無監控 | ⚠️ 待補 |

### 2.4 WebSocket 層

| 指標 | 結果 | 判斷 |
| :--- | :--- | :--- |
| 最大同時連線數 | | |
| Broadcast Loss Rate | | 目標 < 1% |
| Broadcast Throughput | | |

### 2.5 資料庫層（目前無直接 Prometheus 指標，請手動查詢）

```sql
-- Active connections
SELECT count(*) FROM pg_stat_activity WHERE state = 'active';

-- Lock waits
SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock';

-- Slow queries (>500ms)
SELECT query, mean_exec_time, calls
FROM pg_stat_statements
ORDER BY mean_exec_time DESC LIMIT 10;
```

| 指標 | 結果 | 判斷 |
| :--- | :--- | :--- |
| Active connections | | |
| Lock wait count | | 目標 0 |
| Slowest query (mean_exec_time) | | 目標 < 100ms |

### 2.6 ECS / 基礎設施（僅 staging / ECS 環境）

| 指標 | 結果 | 判斷 |
| :--- | :--- | :--- |
| Gateway CPU utilization | | 目標 < 70% |
| Gateway Memory utilization | | 目標 < 80% |
| Order Service CPU | | |
| RDS CPU | | 目標 < 70% |
| Redis Memory | | |

---

## 3. Correctness Audit

> 每輪壓測後必填。audit 失敗代表整輪測試失敗。

### 3.1 資產守恆

```sql
-- 確認 balance + locked 加總不變
SELECT
  currency,
  SUM(balance) AS total_balance,
  SUM(locked)  AS total_locked,
  SUM(balance + locked) AS total
FROM accounts
GROUP BY currency;
```

| 幣種 | 壓測前 total | 壓測後 total | 差異 | 判斷 |
| :--- | :--- | :--- | :--- | :--- |
| BTC | | | | ✅ 一致 / ❌ 不一致 |
| USDT | | | | |

### 3.2 Balance + Locked 一致性

```sql
-- 找出 locked > 0 但沒有對應 NEW / PARTIALLY_FILLED order 的帳戶
SELECT a.user_id, a.currency, a.locked
FROM accounts a
WHERE a.locked > 0
  AND NOT EXISTS (
    SELECT 1 FROM orders o
    WHERE o.user_id = a.user_id
      AND o.status IN ('NEW', 'PARTIALLY_FILLED')
  );
```

| 結果 | 是否為 0 筆 | 判斷 |
| :--- | :--- | :--- |
| 異常帳戶數 | | ✅ / ❌ |

### 3.3 Stuck Order 稽核

```sql
-- 找出超過壓測時間仍為 NEW 的訂單（調整時間條件）
SELECT id, user_id, symbol, status, created_at
FROM orders
WHERE status IN ('NEW', 'PARTIALLY_FILLED')
  AND created_at < NOW() - INTERVAL '10 minutes'
ORDER BY created_at ASC;
```

| 結果 | stuck order 數量 | 判斷 |
| :--- | :--- | :--- |
| 異常訂單數 | | 目標 0 |

### 3.4 Trade / Order 對帳

```sql
-- 確認每筆 FILLED order 對應的 trade 總量是否一致
SELECT
  o.id AS order_id,
  o.filled_quantity,
  COALESCE(SUM(t.quantity), 0) AS trade_total,
  o.filled_quantity - COALESCE(SUM(t.quantity), 0) AS diff
FROM orders o
LEFT JOIN trades t ON (t.maker_order_id = o.id OR t.taker_order_id = o.id)
WHERE o.status = 'FILLED'
GROUP BY o.id, o.filled_quantity
HAVING ABS(o.filled_quantity - COALESCE(SUM(t.quantity), 0)) > 0.000001;
```

| 結果 | 對帳不一致筆數 | 判斷 |
| :--- | :--- | :--- |
| 異常筆數 | | 目標 0 |

### 3.5 Correctness Audit 總結

| 項目 | 結果 | 通過 |
| :--- | :--- | :--- |
| 資產守恆 | | ✅ / ❌ |
| balance + locked 一致 | | ✅ / ❌ |
| stuck order 為零 | | ✅ / ❌ |
| trade / order 對帳一致 | | ✅ / ❌ |

**若任一項為 ❌，本輪壓測直接視為失敗，需先修復才繼續。**

---

## 4. 瓶頸與問題摘要

找出這輪的第一個瓶頸（只填最重要的一個）：

| 問題 | 出現條件 | 影響 | 建議動作 |
| :--- | :--- | :--- | :--- |
| | | | |
| | | | |

---

## 5. 與前一輪對比

| 指標 | 前一輪 | 本輪 | 變化 |
| :--- | :--- | :--- | :--- |
| 資料量等級 | | | |
| P95 latency | | | |
| Order TPS | | | |
| 5xx Error Rate | | | |
| Correctness | | | |

---

## 6. 本輪結論

請直接回答以下問題，不能留空：

1. **本輪可以下什麼結論？**（請說明環境、資料量、測試類型後再結論）

   > 範例：在 local 環境、S 級資料量、30 秒 100 VU 下，HTTP P95 latency 為 45ms，correctness audit 全部通過。可以說明 baseline 功能正確，但尚無法對線上容量有任何結論。

   ___

2. **本輪不能下什麼結論？**

   ___

3. **下一步行動**（具體動作，而非方向）：

   - [ ] 
   - [ ] 

---

## 7. 附件

| 附件 | 位置 |
| :--- | :--- |
| k6 結果 HTML | |
| Grafana 截圖 | |
| DB 查詢結果 | |
| correctness audit 輸出 | |
