# 架構演進：從模組化單體到微服務

本文檔紀錄了系統從「模組化單體 (Modular Monolith)」演進至「分散式微服務 (Microservices)」的核心概念與具體程式碼實作，特別關注於**介面隔離**與**通訊機制的無痛抽換**。

---

## 🏗️ 階段一：建立模組化單體 (Modular Monolith)

在系統發展初期，為避免微服務帶來的網路延遲與部署複雜度，通常採用模組化單體架構。

### 核心原則

1. **嚴格領域劃分**：各個模組（如 Order、Matching）分屬不同套件，彼此之間不可直接互相呼叫具體實作。
2. **依賴反轉 (Dependency Inversion)**：模組之間的溝通必須依賴「抽象介面 (Interface)」，而非真實元件。
3. **記憶體通訊**：使用進程內的記憶體機制（如 Go Channel）來傳遞事件。

### 具體實作範例

首先，我們在核心領域定義一個**抽象介面**：

```go
// internal/core/ports.go
type EventPublisher interface {
    Publish(event interface{}) error
}
```

接著，我們實作一個基於 Go Channel 的**記憶體事件匯流排**：

```go
// internal/infrastructure/memory/bus.go
package memory

import "fmt"

type MemoryEventBus struct {
    ch chan interface{}
}

func NewMemoryEventBus(bufferSize int) *MemoryEventBus {
    return &MemoryEventBus{
        ch: make(chan interface{}, bufferSize),
    }
}

// 實作 EventPublisher 介面
func (m *MemoryEventBus) Publish(event interface{}) error {
    select {
    case m.ch <- event:
        return nil
    default:
        return fmt.Errorf("channel is full")
    }
}
```

在 `OrderService` 中，我們**只依賴抽象介面**，完全不知道背後是誰在接信：

```go
// internal/core/order_service.go
type OrderService struct {
    // ... 其他依賴
    publisher EventPublisher
}

func (s *OrderService) PlaceOrder(order *Order) error {
    // 1. 寫入資料庫
    s.db.Save(order)

    // 2. 廣播事件 (完全不在乎背後是記憶體還是網路)
    s.publisher.Publish(OrderPlacedEvent{OrderID: order.ID})

    return nil
}
```

**單一啟動程式：**所有的模組全都在這支程式裡初始化並串聯。

```go
// cmd/monolith/main.go
func main() {
    bus := memory.NewMemoryEventBus(100)

    // 把記憶體匯流排注入給訂單服務
    orderSvc := core.NewOrderService(db, bus)

    // 撮合引擎在同一個程式裡監聽這個頻道
    go matchingEngine.StartListening(bus.Subscribe())
}
```

---

## 🚀 階段二：微服務物理拆分與 Kafka 導入

當流量成長，我們需要將 `OrderService` 與 `MatchingEngine` 部署在不同的機器上。此時，我們先前建立的模組化單體介面將展現出強大的威力。

### 無痛轉換：實作新的 Kafka 匯流排

我們只需要實作一個全新的 `KafkaEventBus`，不需要更改 `OrderService` 任何一行業務邏輯：

```go
// internal/infrastructure/kafka/producer.go
package kafka

type KafkaEventBus struct {
    producer *kafka.Client
}

// 實作同一個 EventPublisher 介面
func (k *KafkaEventBus) Publish(event interface{}) error {
    bytes, _ := json.Marshal(event)
    return k.producer.Produce(&kafka.Message{
        Topic: "exchange.orders",
        Value: bytes,
    })
}
```

### 物理切割：拆分啟動入口

原本的單體 `cmd/monolith/main.go` 被廢棄，取而代之的是獨立的微服務進入點。

**1. 訂單服務微服務：**

```go
// cmd/order-service/main.go
func main() {
    // 1. 初始化 Kafka （換掉原本的 Memory Bus）
    kafkaBus := kafka.NewKafkaEventBus(brokers)

    // 2. 注入給訂單服務 (業務代碼 0 修改！)
    orderSvc := core.NewOrderService(db, kafkaBus)

    // 3. 啟動 HTTP API
    http.ListenAndServe(":8080", nil)
}
```

**2. 撮合引擎微服務：**

```go
// cmd/matching-engine/main.go
func main() {
    // 1. 啟動 Kafka Consumer
    consumer := kafka.NewConsumer("exchange.orders")

    // 2. 引擎透過網路收到事件，而不是 Go Channel
    engine := core.NewMatchingEngine()
    engine.StartProcessing(consumer)
}
```

> [!IMPORTANT]  
> **演進精髓**
> 最完美的架構演進，是由於底層（`OrderService`）強烈依賴了介面（`EventPublisher`）。這讓我們在將「記憶體通訊」升級為「分散式 Kafka 通訊」時，只要在最上層的 `main.go` 像換樂高積木一樣抽換模組即可，這正是依賴反轉 (DIP) 與模組化單體的最大價值。

---

-----以上是理想正規流程-----

-----以下是目前實際狀況-----

## 🛠️ 現行微服務架構的模組化優化

當系統成功過渡至微服務與 Kafka 後，為迎接如「多交易所策略回測平台」等宏大藍圖，我們不再需要退回單體架構，而是針對「現有微服務進行模組化重構（Modularization）」。

### 優化目標：消除大泥球 (Big Ball of Mud)

當前的架構可能將下單、撮合、結算等業務邏輯混雜在共同的核心目錄（如 `internal/core/`）。若直接加入新模組（如 Strategy Engine, CCXT Adapter），將引發嚴重的循環依賴與維護災難。

### 具體的重構行動

1. **目錄結構的大搬風 (領域驅動拆分)**
   打散共用的 `core` 資料夾，針對各微服務的實體邊界，建立獨立且封閉的「王國」：
   - `internal/domain/`：存放所有微服務共用的實體與核心結構（如 `Order`, `Trade`, 工具函式）。
   - `internal/order/`：存放下單邏輯、HTTP Handlers 以及 Outbox Worker。
   - `internal/matching/`：存放純記憶體撮合引擎邏輯。
   - `internal/settlement/`：存放結算與資料庫扣款邏輯。

2. **定義明確的邊界介面 (Ports)**
   在每個領域資料夾內建立宣告檔（例如 `ports.go`），強迫所有外部依賴皆需透過介面定義。
   例如在 `order` 模組中獨立定義 `type EventPublisher interface { ... }`，確保 `order` 模組在編譯期完全不依賴 `matching` 或具體的 `kafka` 套件。

3. **上帝物件 (God Object) 的拆解與瘦身**
   完全移除原本龐大且需要注入多種無關依賴的 `ExchangeService`。
   - 在 `cmd/order-service/main.go` 中，僅實例化 `order.NewService(...)`。
   - 在 `cmd/matching-engine/main.go` 中，僅實例化 `matching.NewEngine()`。

> [!TIP]
> **Monorepo 微服務的最完美型態**
> 帶著現有強大的 Kafka 與 Outbox 基礎，透過資料夾搬移與介面定義，完成「微服務架構內的代碼模組化」。這不僅能確保分散式系統的高吞吐能力，更能享有結構清晰、程式碼純潔、單元測試極易撰寫的完美維護體驗。
