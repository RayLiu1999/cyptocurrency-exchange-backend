---
description: TDD 測試驅動開發流程
---

# TDD 測試驅動開發流程

本專案採用 **TDD TODO List** 方法進行開發，遵循 Red-Green-Refactor 循環。

## 核心原則

```
┌─────────────────────────────────────────────────────────────┐
│  1. 寫下 TODO List (你想要達成的行為清單)                    │
│  2. 選一個最簡單的 TODO，寫一個會失敗的測試 (Red)            │
│  3. 用最少的程式碼讓測試通過 (Green)                         │
│  4. 重構，保持測試綠燈 (Refactor)                            │
│  5. 如果發現新的細節，加入 TODO List                         │
│  6. 重複步驟 2-5，直到 TODO List 清空                        │
└─────────────────────────────────────────────────────────────┘
```

## 開發流程

### Step 1: 建立 TODO List

在開始實作前，先在測試檔案或專屬文件中列出所有預期行為：

```go
// matching_engine_test.go

/*
TODO List - Matching Engine

基本功能：
- [ ] 空的 OrderBook 不應有任何成交
- [ ] 買單價格 >= 賣單價格時應成交
- [ ] 成交價格應為先進場的訂單價格 (Maker Price)
- [ ] 部分成交：大單應拆分成交

價格優先：
- [ ] 買方出價高的優先成交
- [ ] 賣方出價低的優先成交

時間優先：
- [ ] 同價位時，先進場的訂單優先成交

邊界條件：
- [ ] 市價單立即成交或取消
- [ ] 自成交防護 (Self-Trade Prevention)
*/
```

### Step 2: 選擇最簡單的 TODO

從清單中選一個**最簡單、最基礎**的項目開始：

```go
// 選擇：空的 OrderBook 不應有任何成交
func TestMatchingEngine_EmptyOrderBook_NoTrades(t *testing.T) {
    // Arrange
    engine := NewMatchingEngine()
    order := &Order{Side: Buy, Price: decimal.NewFromInt(100), Quantity: decimal.NewFromInt(1)}
    
    // Act
    trades := engine.Match(order)
    
    // Assert
    assert.Empty(t, trades)
}
```

### Step 3: Red → Green → Refactor

```bash
# 1. 執行測試，確認失敗 (Red)
go test -v -run TestMatchingEngine_EmptyOrderBook ./internal/core/matching/

# 2. 寫最少程式碼讓測試通過 (Green)
# 3. 重構，保持綠燈

# 4. 持續執行測試確認
make test
```

### Step 4: 發現細節，更新 TODO List

實作過程中發現新需求或細節，立即加入 TODO List：

```go
/*
TODO List - Matching Engine (更新)

基本功能：
- [x] 空的 OrderBook 不應有任何成交 ✓
- [ ] 買單價格 >= 賣單價格時應成交
- [ ] 成交價格應為先進場的訂單價格 (Maker Price)

  新發現的細節：
  - [ ] OrderBook 需要資料結構來儲存訂單
  - [ ] 買賣單應分開儲存
  - [ ] 需要按價格排序的資料結構

- [ ] 部分成交：大單應拆分成交
...
*/
```

### Step 5: 重複直到完成

繼續循環：選 TODO → 寫測試 → 實作 → 重構 → 更新 TODO

---

## TODO List 管理位置

### 方案 A：測試檔案內 (推薦)

```go
// internal/core/matching/engine_test.go

/*
=== TODO List ===
- [x] 基本成交
- [ ] 價格優先
- [ ] 時間優先
*/
```

### 方案 B：獨立 Markdown 檔案

```
docs/TDD_TODO.md
```

### 方案 C：GitHub Issues + Labels

使用 `tdd-todo` label 追蹤

---

## 實際範例：撮合引擎開發

```go
// internal/core/matching/engine_test.go

package matching

import (
    "testing"
    "github.com/shopspring/decimal"
    "github.com/stretchr/testify/assert"
)

/*
=== TDD TODO List: Matching Engine ===

Phase 1 - 基本成交：
- [ ] 空 OrderBook 收到訂單，訂單進入等待
- [ ] 買單價格 >= 最低賣單價格時成交
- [ ] 成交後更新雙方訂單狀態

Phase 2 - 價格時間優先：
- [ ] 賣方：價格低的優先
- [ ] 買方：價格高的優先
- [ ] 同價位：時間早的優先 (FIFO)

Phase 3 - 部分成交：
- [ ] 訂單數量 > 對手方時，部分成交
- [ ] 剩餘數量留在 OrderBook

Phase 4 - 邊界條件：
- [ ] 市價單 (Market Order)
- [ ] 自成交防護

=====================================
*/

// --- Phase 1: 基本成交 ---

func TestEngine_EmptyBook_OrderEntersBook(t *testing.T) {
    engine := NewEngine("BTC-USD")
    order := NewOrder(Buy, decimal.NewFromInt(100), decimal.NewFromInt(1))
    
    trades := engine.Process(order)
    
    assert.Empty(t, trades, "空 OrderBook 不應產生成交")
    assert.Equal(t, 1, engine.BuyOrderCount(), "訂單應進入買方佇列")
}
```

---

## Makefile 支援

```bash
# 執行特定測試 (TDD 開發時使用)
make test-run TEST=TestEngine_EmptyBook

# 持續監控測試 (搭配 watchexec)
watchexec -e go "make test"
```

---

## 參考資源

- [Test-Driven Development by Example](https://www.amazon.com/Test-Driven-Development-Kent-Beck/dp/0321146530) - Kent Beck
- [The Three Laws of TDD](https://blog.cleancoder.com/uncle-bob/2014/12/17/TheCyclesOfTDD.html) - Robert C. Martin
