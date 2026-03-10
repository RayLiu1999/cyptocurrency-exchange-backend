# 測試文件索引

本目錄集中管理測試策略、測試規格、測試用途與壓測結果相關文件。

## 文件分類

| 文件 | 用途 |
| :--- | :--- |
| `TEST_SPECIFICATION.md` | 盤點目前哪些後端測試已覆蓋、哪些仍缺漏 |
| `TEST_GUIDE.md` | 說明本專案各類測試的目的、適用時機與典型範圍 |
| `NEXT_TEST_PLAN.md` | 定義下一階段要補上的測試優先順序、範圍與落地步驟 |
| `LOAD_TESTING.md` | 說明 k6 壓測策略、腳本設計與執行方式 |
| `AWS_STRESS_TEST_METRICS.md` | 整理壓測結果指標與 AWS 壓測觀察重點 |

## 建議閱讀順序

1. 先讀 `TEST_GUIDE.md`，理解不同測試的責任邊界。
2. 再讀 `TEST_SPECIFICATION.md`，確認目前已覆蓋與未覆蓋範圍。
3. 再讀 `NEXT_TEST_PLAN.md`，確認下一步應優先補哪些測試。
4. 若要執行 smoke/load test，參考 `LOAD_TESTING.md`。