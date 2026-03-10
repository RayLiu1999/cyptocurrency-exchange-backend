# 後端測試規格與現況盤點

本文件依據目前專案中的實際測試檔進行盤點，目的是回答兩件事：

1. 目前哪些核心行為已經有測試覆蓋。
2. 哪些高風險功能仍未被測到，不能誤判為已完成。

目前可確認的測試檔如下：

- `internal/core/matching/engine_test.go`
- `internal/api/handlers_test.go`
- `internal/core/order_service_test.go`
- `internal/core/exchange_service_test.go`
- `scripts/k6/smoke-test.js`

## 測試技術選型

| 類別 | 工具/庫 | 用途 |
| :--- | :--- | :--- |
| 測試框架 | `testify/assert` | 驗證輸出結果與狀態 |
| Mock 工具 | `testify/mock` | 隔離 Repository 與 Service 依賴 |
| 數值模型 | `shopspring/decimal` | 避免價格與數量使用浮點數造成誤差 |
| HTTP 測試 | `net/http/httptest` | 驗證 Gin 路由與回應 |
| 路由框架 | `gin-gonic/gin` | 以 TestMode 驗證 API Handler |
| 冒煙測試 | `k6` | 驗證服務啟動後的核心 API 流程是否可用 |

---

## 整體測試目標

後端測試應至少覆蓋以下四個層級：

| 層級 | 目標 |
| :--- | :--- |
| 單元測試 | 驗證撮合規則、訂單狀態轉換、資金結算等純業務邏輯 |
| API 測試 | 驗證路由、參數解析、回應碼與 JSON 結構 |
| 冒煙測試 | 驗證服務啟動後的核心交易 happy path 是否可實際跑通 |
| 整合測試 | 驗證 Service 與資料庫、交易快照、交易流程在真實依賴下的表現 |

---

## 實際覆蓋現況

### 1. 單元測試：撮合引擎 (`internal/core/matching/`)

這一層是目前覆蓋最完整的部分。

| 測試主題 | 內容 | 狀態 |
| :--- | :--- | :--- |
| 基礎結構 | 建立 OrderBook、買賣單分流、空簿掛單 | ✅ 已完成 |
| 基本成交 | 價格可成交、Maker 價格成交、完全成交後移除 | ✅ 已完成 |
| 價格優先 | 最低賣價優先、最高買價優先 | ✅ 已完成 |
| 時間優先 | 同價位 FIFO 順序 | ✅ 已完成 |
| 部分成交 | Maker 剩餘、Taker 剩餘、剩餘量留在簿上 | ✅ 已完成 |
| 連續成交 | 大單連續吃多個對手方 | ✅ 已完成 |
| 市價單 | 市價買、市價賣、多筆成交、流動性不足 | ✅ 已完成 |
| 多交易對 | 不同 symbol 隔離、同 symbol 成交、GetEngine 單例 | ✅ 已完成 |
| 邊界條件 | 價格不匹配、自成交防護 | ✅ 已完成 |

### 2. 單元測試：Service 層 (`internal/core/`)

這一層目前不是整合測試，而是 Mock-based 業務邏輯測試。

| 測試主題 | 內容 | 狀態 |
| :--- | :--- | :--- |
| 下單資金檢查 | 餘額不足返回錯誤 | ✅ 已完成 |
| 下單成功流程 | 建立訂單、初始狀態、呼叫 CreateOrder | ✅ 已完成 |
| 取消訂單 | 成功取消、已成交不可取消、非本人不可取消 | ✅ 已完成 |
| Maker 狀態更新 | filled quantity 更新、完全成交狀態切換 | ✅ 已完成 |
| 結算邏輯 | 買方與賣方的解鎖與餘額更新次數 | ✅ 已完成 |
| 交易事務失敗回滾 | `ExecTx` 失敗（CreateOrder 寫入錯誤）、ProcessTrade 步驟失敗後續未執行 | ✅ 已完成 |
| 快照恢復（Mock） | `RestoreEngineSnapshot` 成功重建 OrderBook、資料庫讀取失敗返回錯誤 | ✅ 已完成 |
| 真實資料庫互動 | PostgreSQL repository 與 service 的實際整合 | ❌ 尚未覆蓋 |

### 3. API Handler 測試 (`internal/api/`)

這一節僅統計 `handlers_test.go` 這類 Go 測試，**不包含** k6 冒煙測試對執行中服務的 happy path 驗證。

| API 模組 | 內容 | 狀態 |
| :--- | :--- | :--- |
| `POST /orders` | 缺少必要欄位返回 `400`、成功建立返回 `201` | ✅ 已完成 |
| `GET /orders/:id` | 查詢單筆訂單返回 `200` | ✅ 已完成 |
| `GET /orders?user_id=...` | 查詢用戶訂單列表返回 `200` | ✅ 已完成 |
| `DELETE /orders/:id` | 成功取消、缺少 `user_id`、非法 UUID、Service 回錯 | ✅ 已完成 |
| `GET /orderbook` | 訂單簿快照正常回傳、Service 失敗返回 `500` | ✅ 已完成 |
| `GET /accounts` | 餘額查詢成功、缺少 `user_id`、非法 UUID、Service 回錯 | ✅ 已完成 |
| `GET /klines` | K 線資料正常回傳 | ✅ 已完成 |
| `GET /trades` | 最近成交正常回傳 | ✅ 已完成 |
| `POST /test/join` | 建立匿名測試帳號成功返回 `201`、Service 回錯返回 `500` | ✅ 已完成 |
| `POST /simulation/start` | 未注入 simulator 返回 `503` | ✅ 已完成 |
| `POST /simulation/stop` | 未注入 simulator 返回 `503` | ✅ 已完成 |
| `GET /simulation/status` | 未注入 simulator 返回 `503` | ✅ 已完成 |
| `DELETE /simulation/data` | 清除成功返回 `200`、Service 回錯返回 `500` | ✅ 已完成 |
| 錯誤處理 | 無效 UUID、Service 回錯、非法 query param 均已覆蓋 | ✅ 已完成 |

### 4. 冒煙測試 (`scripts/k6/smoke-test.js`)

目前專案已具備一支統一的 k6 冒煙測試腳本，作為服務啟動後的最小可運行驗證；此層主要覆蓋核心 happy path，不取代 Handler 單元測試，也不等同於真實資料庫整合測試。

| 測試主題 | 內容 | 狀態 |
| :--- | :--- | :--- |
| 測試帳號建立 | `POST /test/join` 建立匿名測試帳號 | ✅ 已完成 |
| 帳戶查詢 | `GET /accounts` 驗證帳戶查詢可用 | ✅ 已完成 |
| 訂單簿查詢 | `GET /orderbook` 驗證市場快照端點可用 | ✅ 已完成 |
| 下單流程 | `POST /orders` 建立限價單並驗證回傳狀態 | ✅ 已完成 |
| 查單流程 | `GET /orders/:id`、`GET /orders?user_id=...` 驗證查詢流程 | ✅ 已完成 |
| 取消流程 | `DELETE /orders/:id` 驗證取消訂單 happy path | ✅ 已完成 |
| 覆蓋限制 | 錯誤路徑、異常資料、競態與資料持久化一致性 | ❌ 尚未覆蓋 |

### 5. 真正的整合測試 (Integration)

目前專案中沒有可確認的真實整合測試；`k6` 冒煙測試不屬於此類，以下項目都還不能標示為已完成或進行中：

| 測試主題 | 內容 | 狀態 |
| :--- | :--- | :--- |
| Repository + PostgreSQL | 真實 DB 建立、查詢、更新、刪除 | ✅ 已完成 |
| 端對端下單流程 | HTTP -> Service -> Repository -> Matching -> Trade Persistence | ❌ 尚未覆蓋 |
| 快照恢復 | 系統重啟後從 DB 恢復掛單狀態 | ❌ 尚未覆蓋 |
| 併發下單 | 多用戶同時下單與競態條件驗證 | ❌ 尚未覆蓋 |

---

## 關鍵測試重點是否有被測到

### 已被測到的重點

| 重點 | 說明 |
| :--- | :--- |
| 價格優先 | 已驗證最優價格先成交 |
| 時間優先 | 已驗證同價位先進先出 |
| 部分成交 | 已驗證 Maker 與 Taker 的剩餘量行為 |
| 市價單 | 已驗證市價單掃單與流動性不足處理 |
| 多交易對隔離 | 已驗證不同 symbol 不互相撮合 |
| 下單與查單 API 基本路徑 | 已驗證最基本 happy path |
| 核心 HTTP 流程冒煙驗證 | 已驗證開戶、查餘額、查簿、下單、查單、取消訂單的服務可用性 |
| 取消訂單核心業務邏輯 | 已在 Service 層與 API 層雙重驗證 |
| 成交結算 | 已驗證解鎖與餘額更新流程 |
| 交易事務失敗回滾 | 已驗證 `ExecTx` 失敗不殘留半成品、ProcessTrade 步驟失敗中止後續執行 |
| 快照恢復（Mock） | 已驗證活動訂單成功重建至記憶體撮合引擎，以及資料庫失敗時的錯誤回傳 |

### 尚未被測到的高風險重點

| 重點 | 風險 |
| :--- | :--- |
| 價格不匹配不成交 | 直接影響撮合正確性 |
| 自成交防護 | 直接影響交易公平性與風控 |
| API 錯誤路徑 | 容易在非法輸入或資料缺失時回錯誤狀態碼 |

| 快照恢復（真實 DB） | Mock 層已驗證，但重啟後的真實 PostgreSQL 快照一致性仍未驗證 |
| 真實資料庫整合 | ✅ 已完成：Account/Order/Trade Repository 均已覆蓋，ExecTx 回滾行為通過驗證 |
| 併發場景 | 無法證明沒有 race condition 或重複扣款 |

---

## 建議下一步

Phase 1–4 均已完成。整合測試階段並發現並修正一個 **資金洩漏 Bug**（`UnlockFunds` 未正確還原 `balance`，已於 `account_repository.go` 修正）。

1. **端對端下單流程**：HTTP → Service → Repository → Matching → Trade Persistence 的完整鏈路驗證。
2. **併發與競態**：確認多用戶同時下單不會發生重複扣款或 race condition。
3. **真實快照恢復（DB + Engine）**：確認服務重啟後，真實 PostgreSQL 活動訂單能正確重建撮合引擎狀態。