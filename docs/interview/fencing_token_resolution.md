# Fencing Token 與防腦裂 (Split-Brain) 架構演進紀錄

## 📌 背景與問題描述

在微服務架構中，`matching-engine` (撮合引擎) 是有狀態的 (Stateful)，我們必須確保同一時間只有「一個」實例 (Leader) 在處理同一個交易對，否則會引發重複撮合與連環財務錯誤。

為此，我們實作了基於 PostgreSQL 的 **Active-Passive Leader Election (選主機制)**。但僅靠租約 (Lease) 與 TTL 是遠遠不夠的，因為系統會面臨嚴重的 **「腦裂 (Split-Brain) 與殭屍 (Zombie)」漏洞**：

**💥 災難情境：**
1. Host A 是 Leader，正在處理訂單。
2. Host A 遭遇了長時間的 **GC Pause (垃圾回收停頓)** 或 **網路分區 (Network Partition)**。
3. Host A 的租約過期，Host B 成功競選成為新 Leader，並開始處理新的訂單。
4. Host A 從停頓中甦醒。它**不知道自己已經被罷免**，記憶體中的程式碼繼續執行，拿著「舊的掛單簿」完成了一次撮合併發佈給下游的 `order-service`。
5. `order-service` 無從分辨這條訊息是真 Leader 還是殭屍發出來的 ➔ **發生雙重花費 (Double Spending) 或錯誤結算**。

---

## 🛠️ 第一階段：引入 Fencing Token (護照驗證)

為了解決上述問題，我們參考了分散式系統的經典作法：**Fencing Token**。
* 每當有一台新機器當選 Leader，資料庫就會核發一個「單調遞增」的版本號 (FencingToken)。
* Leader 在發送任何 `SettlementRequestedEvent` 時，都必須在 Kafka 訊息中夾帶這個 Token。
* 下游 `order-service` 在收到結算事件時，先去資料庫 `SELECT` 比對目前合法的 Token。如果訊息中的 Token 小於資料庫內的，則判定為「舊朝遺詔（殭屍訊息）」，予以丟棄。

---

## 🚨 第二階段：修復驗證空窗期 (DB Race Condition)

雖然我們有了 Token，但在初步審核時，我們發現了一個**極細微的 Race Condition 破綻**：

`order-service` 原本是在「開啟資料庫交易 (Transaction) **之前**」去驗證 Token。
這產生了一個微秒級的空窗期：
```text
(1) order-service 驗證 Token = 2 ➔ 通過！
(2) ----------- 系統發生 GC 停頓了 2 秒 -----------
(3) Host B 竄位，資料庫 Token 變成 3。
(4) order-service 甦醒，由於剛才已經「口頭」驗證過了，直接執行餘額 UPDATE ➔ 資金再次出錯！
```

**✅ 解決方案：將驗證推入 DB Transaction 並施加鎖 (`FOR SHARE`)**
我們參考了高頻交易引擎 `AXS` 的樂觀鎖設計，將 `ValidateFencingTokenTx` 推入到處理餘額的同一個 DB Transaction 內，並加上 `FOR SHARE` 鎖：
```sql
SELECT fencing_token FROM partition_leader_locks WHERE partition = $1 FOR SHARE
```
這宣告了：「在 `order-service` 完成結算前，絕對不允許任何人篡位修改 Token。」這達成了 **100% 的原子性 (Atomicity) 與殭屍免疫**。

---

## 🔍 第三階段：補齊微觀遺漏 (撤單與快取漏洞)

在徹底確保了「結算」路徑的安全後，我們進行了最後的全盤審計，並排除了兩個致命的隱患：

### 1. 撤單路徑 (Cancel Order) 的漏洞
* **問題**：原先只有結算帶有 Token，但 `OrderCanceledEvent` (使用者撤單) 卻未受防護。這意味著殭屍引擎雖然無法結算，卻可以向 `order-service` 發出錯誤的「撤單命令」，將一筆正在排隊的合法訂單解鎖並取消。
* **修補**：
  - 更新 `OrderCanceledEvent` 結構，加入 `FencingToken` 欄位。
  - 在 `matching-engine` 發布撤單事件時夾帶令牌。
  - 在 `order-service` 的 `handleOrderCanceled` 事務的最開頭，同樣施加 `FOR SHARE` 原子驗證。

### 2. 快取幽靈 (Redis Cache Ghosting)
* **問題**：`matching-engine` 每當掛單簿有變動，就會拍下 `OrderBookSnapshot` 寫入 Redis 供前端 WebSockets 或 API 查詢。若殭屍引擎甦醒，它可能會執行 `Set()` 覆蓋掉新 Leader 更新好的掛單簿，造成前端畫面閃爍、出現不存在的幽靈掛單。
* **修補**：
  - 捨棄原生的 Redis `SET` 指令，改用 **Lua Script 實現 Last-Write-Wins (LWW)**。
  - 從 `matching-engine` 傳出的 Snapshot 身上帶上 Fencing Token。
  - 在執行 Redis 寫入時，Lua 腳本會使用 `cjson.decode` 即時解析 Redis 內既有的快照 Token，**只有在「新 Token >= 舊 Token」時才允許寫入**。
  - 此舉做到了 Network 0 Latency 的原子比較，徹底杜絕了快取污染。

---

## 🏆 總結與架構價值

透過以上的演進，我們的 `matching-engine` 高可用架構具備了三個層面的絕對防禦：
1. **財務結算防護**：PostgreSQL 事務內 `FOR SHARE` 鎖定，防禦雙花。
2. **訂單狀態防護**：撤單邏輯的事件攔截，防止惡意解鎖。
3. **資料展示防護**：Redis Lua Script 原子防空洞，保持大盤畫面一致。

這個階段的重構展示了對 **並發處理 (Concurrency)**、**資料庫鎖 (Database Locks)** 以及 **分散式一致性 (Distributed Consensus)** 的深刻理解，是將單機系統升級為企業級分散式系統的關鍵分水嶺。
