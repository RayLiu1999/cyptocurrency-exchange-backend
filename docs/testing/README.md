# 測試文件索引

本目錄包含本交易所後端的完整測試文件體系。每份文件的責任邊界明確，不在多份文件重複描述同一件事。

## 主文件

| 文件 | 用途 |
| :--- | :--- |
| `LOCAL_TESTING.md` | 本地測試清單、責任邊界、資料量等級與本地驗收標準 |
| `ECS_TESTING.md` | staging / ECS 壓測順序、觀測指標、correctness audit、資料量分層與韌性測試 |
| `TEST_EXECUTION_RUNBOOK.md` | 實作步驟、前置條件、執行順序、各測試類型監控指引與結果整理方式 |
| `TEST_REPORT_TEMPLATE.md` | 每輪測試結果報告模板，包含效能、correctness audit SQL、附件清單 |
| `grafana/README.md` | Grafana dashboard 說明：已具備指標、尚未具備指標（PostgreSQL / Redis / Go runtime / Kafka lag）、各測試類型重點 panel |

## 目前能力摘要

| 測試類型 | 目前狀態 |
| :--- | :--- |
| Go 單元 / integration / race | ✅ 已具備 |
| k6 baseline（smoke / load / spike / ws-fanout） | ✅ 已具備 |
| ECS 部署與手動壓測 | ✅ 已具備 |
| correctness audit SQL | ⚠️ SQL 已整理於 `TEST_REPORT_TEMPLATE.md`，尚未腳本化 |
| 資料量分層（S/M/L/XL）生成器 | ❌ 尚無 |
| 韌性 / chaos 測試工具 | ❌ 尚無 |
| Soak test 自動化 | ⚠️ 可手動延長 k6，尚未形成完整套件 |
| PostgreSQL / Redis / Kafka lag metrics | ❌ Grafana 尚無，需手動查詢 |
| Go runtime metrics（goroutine / heap） | ❌ 尚未採集 |

## 舊文件導向

以下舊檔名目前只保留導向用途，避免既有連結失效：

| 舊文件 | 新主文件 |
| :--- | :--- |
| `TEST_GUIDE.md` | `LOCAL_TESTING.md` |
| `TEST_SPECIFICATION.md` | `LOCAL_TESTING.md` |
| `LOAD_TESTING.md` | `TEST_EXECUTION_RUNBOOK.md` |
| `NEXT_TEST_PLAN.md` | `TEST_EXECUTION_RUNBOOK.md` |
| `AWS_STRESS_TEST_METRICS.md` | `ECS_TESTING.md` |

| 舊指南 | 新主文件 |
| :--- | :--- |
| `docs/guides/ECS_LOADTEST_GUIDE.md` | `docs/testing/ECS_TESTING.md` |

## 建議閱讀順序

1. 先讀 `LOCAL_TESTING.md`，確認本地該做哪些事情。
2. 再讀 `ECS_TESTING.md`，確認哪些結論一定要放到 staging / ECS 驗證。
3. 要實際執行時，照 `TEST_EXECUTION_RUNBOOK.md` 跑。