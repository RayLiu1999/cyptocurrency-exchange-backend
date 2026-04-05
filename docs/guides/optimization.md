# 撮合引擎效能優化：Heap 與業界標準資料結構比較

在討論撮合引擎的效能優化時，**堆積（Heap / Priority Queue）** 經常被提及，因為它能將尋找「最優價格」的複雜度降低。然而，頂尖交易所（如 Binance 或 LMAX）很少單獨使用 Heap。以下分析 Heap 的應用情境及其在撮合引擎中的利弊。

## 1. 為什麼會想到 Heap？

撮合過程中常見操作：

- **Insert**：加入新訂單
- **Find Best**：找出最便宜的賣單或最高價的買單
- **Delete Best**：成交後移出最優訂單

Heap 在這三項操作的時間複雜度：

| 操作         | Heap 複雜度   | Slice + Sort 複雜度 |
|--------------|---------------|---------------------|
| 插入 (Insert) | $O(\log N)$   | $O(N \log N)$       |
| 尋找最優值   | $O(1)$        | $O(1)$              |
| 刪除最優值   | $O(\log N)$   | $O(N \log N)$       |

> 備註：Slice 擴容可能導致記憶體重新分配，效能更差。

## 2. Heap 的致命缺點

- **無法保證時間優先 (FIFO)**：Heap 只能保證「價格」優先，若多個訂單價格相同，無法保證先進先出。雖可用 (Price, Timestamp) 複合鍵解決，但會增加實作複雜度。
- **取消訂單 (Cancel Order) 極慢**：Heap 中尋找並刪除特定 Order ID 為 $O(N)$，對高頻撤單系統是效能瓶頸。

## 3. 正規交易所的「標配」資料結構

業界通常結合多種資料結構，實現 Price-Time Priority 的 $O(1)$ 或 $O(\log M)$ 操作（$M$ 為價格層級數量）：

| 結構名稱         | 技術細節                                                         | 優點                         | 缺點             |
|------------------|------------------------------------------------------------------|------------------------------|------------------|
| 價格層級地圖     | HashMap / B-Tree（Go 可用 TreeMap 套件），Key 為價格，Value 為訂單隊列 | 快速定位價格層級             | 實作較複雜       |
| 訂單隊列         | 每個價格層級用雙向鏈結串列（Doubly Linked List）                 | 天然滿足 FIFO，$O(1)$ 刪除   | 需額外維護指標   |
| 訂單索引         | 另維護 map[OrderID]*Order，快速定位訂單                          | $O(1)$ 撤單                  | 記憶體消耗增加   |

## 4. 專案改進建議

你聽到的 "Heap" 通常用於只需追蹤「當前最優價格」的場景。若要優化 `orderbook.go`，建議演進路線：

1. **初級**：Slice + Sort（$O(N \log N)$，適合教學或訂單量極小系統）
2. **進階**：TreeMap (Red-Black Tree) 作為 Order Book，尋找最優價格 $O(\log M)$
3. **極限**：Price Map + Linked List

### Go 實作結構範例

```go
type PriceLevel struct {
   Price  decimal.Decimal
   Orders *list.List // 雙向鏈結串列，保證 FIFO
}

type OrderBook struct {
   bids *treemap.Map // Key: Price, Value: *PriceLevel (價格降序)
   asks *treemap.Map // Key: Price, Value: *PriceLevel (價格升序)
}
```

## 5. 下一步行動指引

- 研究 [treemap](https://pkg.go.dev/github.com/emirpasic/gods/maps/treemap) 套件用法
- 重構 `orderbook.go`，將訂單資料結構改為 Price Map + Linked List
- 加入訂單索引 map[OrderID]*Order，實現 $O(1)$ 撤單
- 撰寫單元測試，驗證撮合與撤單效能

如需進一步規劃 OrderBook 重構，歡迎提出需求。

