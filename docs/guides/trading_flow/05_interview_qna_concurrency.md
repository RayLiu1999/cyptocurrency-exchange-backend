# 交易所系統設計：併發安全與常見雷區 (面試重點 Q&A)

在建置加密貨幣交易所的撮合與結算引擎時，我們遭遇了數個金融系統常見的「高併發交易陷阱」。這份文件整理了我們在開發過程中的實戰經驗與解法，非常適合作為後端系統設計面試的亮點與討論素材。

---

## 1. 跨資源死鎖問題 (Cross-Resource Deadlock)

### 📌 面試官可能問：
> 「如果一個買家 (Taker) 同時掃掉了三個賣家 (Maker) 的掛單，但同時又有另一個大戶也在瘋狂掃單，你的系統會不會發生 Transaction Deadlock？你要怎麼解決？」

### 💥 當時遇到的問題 (The Trap)：
在初版的寫法中，我們取得撮合引擎的 `Trades` (成交紀錄) 後，直接用一個 `for` 迴圈依序對每張 Maker 訂單下 `SELECT ... FOR UPDATE`，然後更新使用者帳戶餘額。
這導致了嚴重的交叉死鎖 (`40P01 Deadlock`)：
*   **Taker A** 正在吃 **Maker 1** 和 **Maker 2**。
*   **Taker B** 正在吃 **Maker 2** 和 **Maker 1**。
*   Taker A 開事務鎖住了 Maker 1 準備鎖 Maker 2；Taker B 鎖住了 Maker 2 準備鎖 Maker 1。兩者互相等待，資料庫報錯。

### 🛠解法 (The Solution)：Two-Phase Locking (兩階段鎖定) 與資源排序
我們引入了**全局排序**的概念。將原本邊算邊寫入的邏輯拆解為兩個階段：
1.  **收集與排序**：先收集所有牽涉到的 Order ID，並依據 UUID 的字串字典序 (`sort.Slice`) 進行排序。
2.  **依序上鎖**：嚴格依照排序後的結果，依序發出 `FOR UPDATE` 鎖定。
由於不管多高的並發、不管是買單還是賣單，系統取鎖的順序永遠是一致的（例如：一定先鎖 ID 小的，再鎖 ID 大的），因此徹底消除了「交叉等待」的空間，從架構上根絕了死鎖。

---

## 2. 遺失更新與狀態覆寫 (Lost Update)

### 📌 面試官可能問：
> 「在並發環境下，如果有兩個 Request 同時對同一筆訂單進行結算，會不會發生『覆蓋別人更新』的 Lost Update 問題？你怎麼保證最後的成交數量是對的？」

### 💥 當時遇到的問題 (The Trap)：
在處理 Taker 訂單時，我們拿著一開始建立時的「舊訂單快照 (`order`)」變數參與整場結算運算，並在最後將算出來的 `FilledQuantity` 寫回資料庫。
*假設情境*：Taker A 送出了一張巨大限價單。第一部分吃掉了一些單，第二部分變成掛單被 Taker B 吃了。
如果處理 Taker A 與 Taker B 的事務在極短毫秒內同時運行，Taker B 先更新了 Taker A 訂單的狀態（量變成了 5），但 Taker A 的程式碼結算完畢後，把舊的快照寫回去（量變成了 3），此時 Taker B 的成交就憑空消失了 (Lost Update)。

### 🛠解法 (The Solution)：將 Taker 納入鎖定池並「重新讀取」
這是一個經常被忽略的細節：我們不僅要鎖定 Maker，**Taker 自己也必須加入 `FOR UPDATE` 的排序與鎖定陣列中**。
```go
makerOrderIDsMap[order.ID] = true // 把 Taker 自己也加入鎖定名單
```
在事務進入階段，我們強迫 Taker 重新從資料庫讀取最新的真實狀態 (`takerOrder := lockedOrders[order.ID]`)。
後續的所有「成交量累加」與「狀態判斷」，都強制在拿到了排他鎖 (![Locking](https://upload.wikimedia.org/wikipedia/commons/thumb/c/c5/Lock_icon.svg/15px-Lock_icon.svg.png)) 的最新物件上執行。這保證了所有的運算都是互相堆疊的，絕不會有舊快照覆蓋新資料的可能。

---

## 3. 隱性耦合與過期快照 (Stale Snapshot Propagation)

### 📌 面試官可能問：
> 「在進行資金計算時，你有沒有遇過因為傳入了過期指標而潛藏的 Bug？如何進行防禦性編程？」

### 💥 當時遇到的問題 (The Trap)：
你發現 `CalculateTradeSettlement(trade, order, makerOrder)` 這裡的 `order` 傳的是舊指標。雖然當下函式內部只使用了不會變動的 `Price` 和 `Side`，這並未造成實際錯誤，但在系統持續迭代的過程中，這是個「隱形地雷」。如果未來有同事修改結算邏輯，加入了對 `FilledQuantity` 的讀取，就會得到過期資料並計算出錯誤金額。

### 🛠解法 (The Solution)：嚴格的資料來源紀律
我們將變數引用修改為 `takerOrder`（從 DB 帶鎖獲取的最新物件），並且釐清了**結算操作的嚴格順序**：
```go
// 1. 先用「最新」的 DB 狀態去計算資金
updates, err := s.CalculateTradeSettlement(trade, takerOrder, makerOrder)

// 2. 算完這筆交易後，再把這次的變動「疊加」上去
takerOrder.FilledQuantity = takerOrder.FilledQuantity.Add(trade.Quantity)
```
這展現了軟體工程中對於「狀態突變 (State Mutation)」的嚴謹管控，是一個很好的架構 Sense。

---

## 4. 資金結算的帳戶死鎖 (Account Overlap Deadlock)

### 📌 面試官可能問：
> 「除了訂單表的死鎖，你的帳戶餘額表遇到多對多交易時，會不會死鎖？怎麼防範？」

### 💥 當時遇到的問題 (The Trap)：
一筆買賣交易牽涉到 4 個資金變動（買方扣美金/得比特幣，賣方得美金/扣比特幣）。
當一個大 Taker 吃掉 10 個 Maker 時，會有高達 40 個 `AccountUpdate`。如果一條一條對資料庫下 `UpdateBalance`，依然會遇到與上面第一點相同的 PostgreSQL 行鎖交叉等待問題。

### 🛠解法 (The Solution)：記憶體先行聚合與二次排序
我們實作了 `AggregateAndSortAccountUpdates()`。
1.  **記憶體聚合 (Aggregation Maps)**：把同一個 UserID 在同一個 Currency 的所有加減密碼學帳操作，先在 Map 裡加總。這將 DB I/O 從 40 次大幅降低至 3~5 次。
2.  **資源過濾 (Filtering)**：如果加總出來的 Balance 和 Locked 變化剛好是 0，直接捨棄，不戳資料庫。
3.  **二次字典排序 (Sorting by Context)**：轉為陣列後，依據 `UserID + Currency` 再次進行全局排序。這確保了最後執行 `accountRepo.UpdateBalance()` 的事務鎖定順序永遠是「由小排到大」，滴水不漏地防禦了帳戶表的死鎖。

---

## 5. 無成交通知漏洞 (WebSocket Event Omission)

### 📌 面試官可能問：
> 「你如何保證所有訂單狀態的轉移都能 100% 透過 WebSocket 傳到前端？有沒有遇過漏傳的情況？」

### 💥 當時遇到的問題 (The Trap)：
在將系統重構為「有成交才進入結算事務」的結構時，不小心刪除或遺漏了 `needsSettlement == false` 路徑下的推播。導致「純限價單掛單，直接進 Orderbook 排隊」的單子，在前端介面上如同消失一般，永遠不會出現 "NEW" 的 UI 狀態更新。

### 🛠解法 (The Solution)：保底分支與統一推播
我們在 `needsSettlement` 的 `else` 分支補回了 `s.tradeListener.OnOrderUpdate(order)`，並確保**掛單簿快照的更新 (`OnOrderBookUpdate`)** 是放在整段 `PlaceOrder` 流程的絕對終點。不管走結算還是排隊分支，只要能影響流動性，最終必定推播即時深度。這體現了處理複雜分散流程時，對邊界條件 (Edge Cases) 的關注度。 
