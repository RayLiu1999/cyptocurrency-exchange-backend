# 交易所系統 — 技術教學文件導覽

> 本系列文件記錄了 `cyptocurrency_exchange` 後端的核心設計與實作細節。
> 每份文件都可獨立閱讀，但建議**依序學習**以獲得最完整的理解。

---

## 學習路徑

```
01 下單HTTP請求  →  02 記憶體撮合引擎  →  03 兩階段結算
                                               ↓
06 Redis深度解析  ←  05 面試Q&A    ←  04 WebSocket廣播
       ↓
07 Kafka事件驅動  →  08 架構設計模式
```

---

## 文件清單

| # | 文件 | 核心主題 | 關鍵概念 |
|---|------|---------|---------|
| 01 | [HTTP 請求與訂單建立](01_order_creation.md) | 如何接受一筆下單請求 | JWT、DTO 驗證、Domain Model |
| 02 | [記憶體撮合引擎](02_matching_engine.md) | 價格優先、時間優先的撮合邏輯 | OrderBook、Mutex、STP |
| 03 | [兩階段結算](03_two_phase_settlement.md) | TX1 鎖資金 + TX2 原子結算 | FOR UPDATE、2PL、Dead Lock |
| 04 | [WebSocket 廣播](04_websocket_broadcast.md) | 即時推播成交與掛單簿更新 | Goroutine、Channel、Event Fan-out |
| 05 | [面試 Q&A：併發陷阱](05_interview_qna_concurrency.md) | 死鎖、Lost Update、TOCTOU | 實戰問題與解法 |
| 06 | [Redis 深度解析](06_redis_deep_dive.md) | 3 種 Redis 使用場景 | Cache、Lua Script、Idempotency |
| 07 | [Kafka 事件驅動架構](07_kafka_event_driven.md) | 非同步撮合的完整事件流 | At-Least-Once、Manual Commit、指數退避 |
| 08 | [架構設計模式](08_architecture_patterns.md) | 貫穿全系統的設計哲學 | 六角架構、UUID v7、Decimal 精度 |

---

## 快速查找

**如果你想了解…**

- **「下單後錢去哪了？」** → [03 兩階段結算](03_two_phase_settlement.md)
- **「Redis 用來幹嘛？」** → [06 Redis 深度解析](06_redis_deep_dive.md)
- **「撮合是同步還是非同步？」** → [07 Kafka 事件驅動架構](07_kafka_event_driven.md)
- **「怎麼防止雙重結算？」** → [05 面試 Q&A](05_interview_qna_concurrency.md) + [07 Kafka](07_kafka_event_driven.md) §六
- **「為什麼用介面而不直接用 Redis？」** → [08 架構設計模式](08_architecture_patterns.md)
- **「為什麼 UUID 而不是自增 ID？」** → [08 架構設計模式](08_architecture_patterns.md) §三
- **「面試題蒐集」** → [05 面試 Q&A](05_interview_qna_concurrency.md) + 每份文件末尾的**面試重點整理**表格

---

## 整體架構一覽

```
┌──────────────────────────────────────────────────────────────────┐
│                         HTTP Layer (Gin)                          │
│  order_handlers.go  account_handlers.go  websocket_handler.go    │
└───────────────────────────────┬──────────────────────────────────┘
                                │ 呼叫介面（不知道底層實作）
┌───────────────────────────────▼──────────────────────────────────┐
│                       Core Layer (業務邏輯)                       │
│         exchange_service.go  order_service.go  events.go         │
│  matching/engine.go  matching_consumer.go  settlement_consumer.go │
└──────┬──────────────────┬───────────────────────┬────────────────┘
       │                  │                        │
┌──────▼──────┐  ┌────────▼──────┐  ┌─────────────▼──────────────┐
│  Repository │  │ Kafka/Producer │  │     Redis/Cache            │
│ (PostgreSQL)│  │   (franz-go)  │  │   (go-redis/v9)            │
└─────────────┘  └───────────────┘  └────────────────────────────┘
```

**依賴方向規則**：外層永遠依賴內層（`API → Core ← Infra`），Core 層只定義介面（`ports.go`），從不直接 import 外層套件。
