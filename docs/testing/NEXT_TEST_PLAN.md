# 下一階段測試規劃

本文件將目前缺口轉成可執行的測試開發順序，目標是優先補齊「高風險、低成本、可快速驗證核心正確性」的測試項目。

## 規劃原則

| 原則 | 說明 |
| :--- | :--- |
| 先補核心正確性 | 先處理會直接影響撮合結果與交易公平性的測試 |
| 先補 Handler 缺口 | 先把已公開 API 的基本行為與錯誤路徑補齊 |
| 再補 Service 交易回滾 | Mock 測試先把交易失敗與快照恢復補起來 |
| 最後做真整合 | 等前面穩定後，再進 PostgreSQL 整合測試 |

## Phase 1: 撮合引擎高風險缺口

- 目標檔案: `internal/core/matching/engine_test.go`
- 優先度: P0
- 原因: 這一層最接近撮合正確性，一旦出錯會直接影響成交結果。

### 要新增的測試

| 建議測試名稱 | 驗證重點 |
| :--- | :--- |
| `TestEngine_PriceMismatch_NoTradeExecuted` | 買價低於賣價時不得成交，且雙方訂單應保留在簿上 |
| `TestEngine_SelfTrade_Prevented` | 同一個 `user_id` 的買賣單不可彼此成交 |

### 完成標準

1. 不成交時 `trades` 為空。
2. 自成交防護成立時，不得產生成交紀錄。
3. 被拒絕撮合的訂單簿狀態必須符合預期。

## Phase 2: API Handler 測試補齊

- 目標檔案: `internal/api/handlers_test.go`
- 優先度: P1
- 原因: 目前只有下單與查單測試，公開路由的大部分行為都缺少保護。

### 2.1 訂單取消 API

| 建議測試名稱 | 驗證重點 |
| :--- | :--- |
| `TestCancelOrderAPI_Success_Returns200` | 合法 `order_id` 與 `user_id` 可成功取消 |
| `TestCancelOrderAPI_MissingUserID_Returns400` | 缺少 `user_id` 時應返回 `400` |
| `TestCancelOrderAPI_InvalidOrderID_Returns400` | 非法 `order_id` 應返回 `400` |
| `TestCancelOrderAPI_InvalidUserID_Returns400` | 非法 `user_id` 應返回 `400` |
| `TestCancelOrderAPI_ServiceError_Returns400` | Service 回錯時應回傳業務錯誤 |

### 2.2 市場與帳戶 API

| 建議測試名稱 | 驗證重點 |
| :--- | :--- |
| `TestGetOrderBookAPI_Success_ReturnsSnapshot` | 訂單簿快照可正常回傳 |
| `TestGetOrderBookAPI_ServiceError_Returns500` | Service 失敗時應返回 `500` |
| `TestGetBalancesAPI_Success_ReturnsAccounts` | 帳戶列表可正常回傳 |
| `TestGetBalancesAPI_MissingUserID_Returns400` | 缺少 `user_id` 應返回 `400` |
| `TestGetBalancesAPI_InvalidUserID_Returns400` | 非法 `user_id` 應返回 `400` |
| `TestGetBalancesAPI_ServiceError_Returns500` | 餘額查詢失敗應返回 `500` |
| `TestGetKLinesAPI_Success_ReturnsData` | K 線資料正常回傳 |
| `TestGetRecentTradesAPI_Success_ReturnsData` | 最近成交正常回傳 |

### 2.3 測試帳號與模擬器 API

| 建議測試名稱 | 驗證重點 |
| :--- | :--- |
| `TestJoinArenaAPI_Success_Returns201` | 建立匿名測試帳號成功 |
| `TestJoinArenaAPI_ServiceError_Returns500` | 建立測試帳號失敗時返回 `500` |
| `TestClearSimulationDataAPI_Success_Returns200` | 清除模擬資料成功 |
| `TestClearSimulationDataAPI_ServiceError_Returns500` | 清除模擬資料失敗時返回 `500` |
| `TestStartSimulationAPI_SimulatorDisabled_Returns503` | 未注入 simulator 時返回 `503` |
| `TestStopSimulationAPI_SimulatorDisabled_Returns503` | 未注入 simulator 時返回 `503` |
| `TestGetSimulationStatusAPI_SimulatorDisabled_Returns503` | 未注入 simulator 時返回 `503` |

### 完成標準

1. 每個公開路由至少有一個 happy path 測試。
2. 每個需要 `user_id` 或 path param 的路由至少有一個非法輸入測試。
3. Service error 與 validation error 的 HTTP status 要清楚區分。

## Phase 3: Service 層交易失敗與恢復場景 ✅ 已完成

- 目標檔案: `internal/core/order_service_test.go`, `internal/core/exchange_service_test.go`
- 優先度: P1
- 原因: 目前多數測試只覆蓋成功路徑，交易失敗回滾與系統恢復仍是盲區。

### 要新增的測試

| 建議測試名稱 | 驗證重點 |
| :--- | :--- |
| `TestPlaceOrder_ExecTxFails_ReturnsError` | 交易包裝層失敗時應回傳錯誤，且不得殘留半成品 |
| `TestProcessTrade_StepFails_TransactionRollsBack` | 成交過程中的任一步驟失敗時應回滾 |
| `TestRestoreEngineSnapshot_Success_RebuildsActiveOrders` | 從活動訂單重建記憶體撮合引擎 |
| `TestRestoreEngineSnapshot_RepositoryError_ReturnsError` | 快照恢復遇到資料讀取失敗時應返回錯誤 |

### 完成標準

1. 關鍵交易路徑至少有一個失敗回滾測試。
2. 快照恢復至少覆蓋成功與失敗兩條路徑。

## Phase 4: PostgreSQL 整合測試 ✅ 已完成

- 目標檔案: 建議新增 `internal/repository/*_integration_test.go`
- 優先度: P2
- 原因: 只有 Mock 測試，無法證明 repository、transaction 與資料庫 schema 實際可用。

### 建議範圍

| 測試主題 | 驗證重點 |
| :--- | :--- |
| Account Repository | 建立帳戶、查餘額、更新餘額 |
| Order Repository | 建單、查單、查用戶訂單、更新狀態 |
| Trade Repository | 成交紀錄建立與查詢 |
| Transaction Flow | `ExecTx` 中多步驟成功與失敗回滾 |

### 建議執行方式

1. 使用獨立測試資料庫或 Docker PostgreSQL。
2. 每個 test case 自行建立資料並在結束後清理。
3. CI 中以可選 job 執行，避免拖慢每次開發迭代。

## 建議實作順序

1. ~~先完成 `engine_test.go` 的兩個高風險缺口。~~ ✅
2. ~~再補 `handlers_test.go` 的取消訂單與市場資料 API。~~ ✅
3. ~~接著補 Service 層的回滾與快照恢復。~~ ✅
4. 最後再導入 PostgreSQL integration test。

## 本輪建議起手式

目前 Phase 1–3 均已完成，下一步為 Phase 4 PostgreSQL 整合測試：