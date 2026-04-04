# 訂單流程架構

## 🏁 階段一：API 收單與鎖資產（秒回前端）

| 元件 | 職責 |
|------|------|
| gin.Handler (路由) | 收到使用者的 HTTP POST 請求 |
| order_handlers.go | 驗證 JSON 資料格式 |
| order_service.go (PlaceOrder) | 執行訂單初始化邏輯 |

**詳細流程：**
1. **TX1 - 資料庫事務**：鎖定可用餘額（Locked），建立狀態為 NEW 的訂單
2. **寫入 Outbox**：將訂單資訊序列化為 OrderPlacedEvent，存入 outbox 表
3. **立即回覆**：返回 HTTP 200 OK，前端顯示「處理中」狀態

---

## 🚚 階段二：郵差送包裹（Outbox Worker）

**outbox/worker.go**
- 背景常駐程式，定期掃描 outbox 表
- 發現待發送事件，將其推送至 Kafka 的 `exchange.orders` Topic
- 確保事件最終一致性（Eventually Consistent）

---

## 🧠 階段三：大腦撮合（Matching Consumer）

**matching_consumer.go (HandleMatchingEvent)**
- 監聽 Kafka `exchange.orders` Topic
- 呼叫記憶體引擎 `engine.Process()`，與訂單簿進行撮合
- **輸出結果**：
  - 成交紀錄（trades）
  - 訂單狀態變更
  - 生成 SettlementRequestedEvent，推送至 `exchange.settlements` Topic
- WebSocket 廣播：實時更新訂單深度簿

---

## 🏦 階段四：會計部結算開獎（Settlement Consumer）

**settlement_consumer.go (HandleSettlementEvent)**
- 監聽 Kafka `exchange.settlements` Topic
- **TX2 - 原子結算事務**：
  1. 鎖定 Taker 和 Maker 訂單
  2. 執行資產轉移（加密貨幣 / 法幣）
  3. 退回零錢至可用餘額（Unlock）
  4. 更新訂單最終狀態（FILLED / CANCELED）

---

## 📊 架構對比

| 維度 | 單體架構 | 微服務架構 |
|------|---------|----------|
| 響應時間 | HTTP request 內完成 | 秒級回覆（異步背景處理） |
| 並發能力 | 受限於單進程 | 支持海量並發 |
| 故障隔離 | 無 | 各服務獨立故障恢復 |
| 複雜度 | 低 | 高（事件驅動） |

---

## ✨ 核心設計優勢

使用者在階段一即可拿到訂單收據，剩餘流程由背景服務協調完成，實現高效的併發處理。

