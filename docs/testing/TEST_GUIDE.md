# 交易系統測試架構與用途說明

本文件詳細說明本專案中各類測試的定位、實施方式及其在開發生命週期中的用途。

## 1. 單元測試 (Unit Tests)
- **目錄**: [`internal/core/...`](/Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend/internal/core), [`internal/core/matching/...`](/Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend/internal/core/matching)
- **執行指令**: `make test`
- **用途**: 
  - 驗證撮合引擎的「價格-時間」優先算法。
  - 確保訂單狀態在各種交易場景下（部分成交、完全成交、由買轉賣等）正確流轉。
  - 檢查資金鎖定與解鎖的精確性。
- **技術**: `testify/assert`, `shopspring/decimal`。

## 2. API Handler 測試 (API Tests)
- **目錄**: [`internal/api/...`](/Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend/internal/api)
- **執行指令**: `make test` (包含在單元測試中)
- **用途**: 
  - 驗證 RESTful 端點的路由與參數繫結 (Binding)。
  - 確保參數錯誤時回報 400，系統錯誤時回報 500。
  - 使用 Mock Service 隔離業務邏輯與資料庫。
- **技術**: `httptest`, `testify/mock`, `gin.TestMode`。

## 3. 冒煙測試 (Smoke Tests / E2E)
- **腳本**: [`scripts/k6/smoke-test.js`](/Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend/scripts/k6/smoke-test.js)
- **執行指令**: `make smoke-test`
- **用途**: 
  - 在伺服器啟動後執行的快速流程驗證。
  - 模擬從開戶、下單、查單到取消訂單的完整金流路徑。
  - 作為 CI/CD 流程中最後一道防線，確保基本 API 可用。
- **技術**: `k6`。

## 4. 壓力與效能測試 (Stress / Load Testing)
- **文件**: [`docs/testing/LOAD_TESTING.md`](/Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend/docs/testing/LOAD_TESTING.md)
- **執行指令**: 請參閱文件中的腳本
- **用途**: 
  - 驗證系統在高併發交易下的吞吐量 (TPS)。
  - 尋找系統效能瓶頸（如資料庫連線池、記憶體洩漏等）。
- **技術**: `k6`。

## 5. 整合測試 (Integration Testing - 規劃中)
- **用途**: 
  - 驗證 Repository 與真實 PostgreSQL 資料庫的交互。
  - 確保事務 (Transaction) 在出錯時能正確回滾。
