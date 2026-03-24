# 本地測試文件

本文件只回答本地環境的測試問題：

1. 本地適合測什麼。
2. 本地不適合直接下什麼結論。
3. 本地測試清單怎麼跑。
4. 哪些資料量等級適合本地。

## 0. 總目標

本地測試階段的總目標不是直接證明交易所最終容量，而是先把最容易在開發階段修掉的問題收斂完，讓後續 staging / ECS 壓測有可信的起點。

本地階段應達成：

1. 核心交易流程 correctness 初步成立。
2. 壓測腳本與測試命令可重複執行。
3. 小資料量與中資料量下沒有明顯結構性錯誤。
4. race condition、panic、明顯 500 error 已先在本地被排除。
5. 明確知道哪些事情已經能在本地回答，哪些一定要放到線上驗證。

## 0.1 目前已具備與尚未具備

### 已具備

| 項目 | 現況 |
| :--- | :--- |
| Go 單元測試 | 已具備，可透過 `make test` 執行 |
| integration / e2e / concurrency 測試 | 已具備，可透過 `make test-integration` 執行 |
| race detector | 已具備，可透過 `make test-race` 執行 |
| k6 baseline 壓測 | 已具備 `smoke`、`load`、`spike`、`ws fanout` |
| 本地 correctness 初步驗證 | 已具備部分測試覆蓋，可驗證基本流程與部分資金鎖定一致性 |

### 尚未具備

| 項目 | 缺口 |
| :--- | :--- |
| correctness audit 自動化 | 尚無壓測後自動檢查資產守恆、stuck order、trade / order 對帳的 SQL 或腳本 |
| 資料量分層落地 | 文件已定義 S / M / L / XL，但尚無對應資料生成器或固定資料集流程 |
| 本地 M / L 級資料量驗證流程 | 尚無標準化 seed / generator 與結果紀錄模板 |
| 本地結論到線上結論的銜接 | 目前主要靠人工判讀，尚無統一報告格式與 gate |

## 1. 本地測試的角色

本地測試的目標不是證明最終容量上限，而是先用最低成本確認：

1. 功能正確。
2. 壓測腳本正確。
3. baseline latency 與 error rate 合理。
4. 第一輪 query、snapshot、race condition 問題已被發現。

如果這四件事都還沒做完，就不應該把時間花在線上壓測。

## 2. 本地適合做的測試

| 類型 | 適合原因 | 建議內容 |
| :--- | :--- | :--- |
| 單元測試 | 回饋最快、最適合快速修正業務邏輯 | 撮合規則、訂單狀態、結算邏輯 |
| API Handler 測試 | 不需完整基礎設施即可驗證輸入輸出 | path / query / body 驗證、錯誤碼、JSON 結構 |
| 微服務流程測試 | 適合先驗證事件流與 listener 行為 | cache snapshot、event publish、listener routing |
| 小資料量整合測試 | 適合驗證 transaction、rollback、競態保護 | repository、e2e、concurrency 基本路徑 |
| 冒煙測試 | 驗證服務有沒有活著 | happy path API、基本查詢與取消 |
| 短時 baseline 壓測 | 適合先找明顯 API / WS 問題 | smoke、load、spike、ws fanout |
| 小到中資料量測試 | 適合先找查詢、索引、快照恢復第一輪退化 | S / M 級資料量 |

## 3. 本地不適合直接下結論的事情

| 類型 | 原因 |
| :--- | :--- |
| 真實容量上限 | 本地 CPU、記憶體、磁碟、網路條件與線上差異大 |
| 多實例壓測結果 | 本地通常沒有真實 ALB、ECS、跨實例 WS 與 service discovery |
| 大資料量最終瓶頸 | 本地在 `10^7` 以上資料量時容易被硬體限制扭曲結果 |
| 真實網路與故障恢復成本 | Kafka / Redis / PostgreSQL 在本地的失效與恢復成本較失真 |
| 架構決策用結論 | 是否擴容、拆服務、調整 ECS / RDS，不能只靠本地結果 |

## 4. 本地測試清單

以下清單分成兩層：

1. 現在已能直接執行的項目。
2. 文件要求但尚未補齊的項目。

### 4.1 基本 Go 測試

```bash
make test
make test-integration
make test-race
make test-all
```

### 4.2 k6 baseline

```bash
make smoke-test
make load-test
make spike-test
make ws-fanout-test
```

### 4.3 本地 checklist

| 檢查項目 | 驗收標準 |
| :--- | :--- |
| 單元測試 | 核心撮合、結算、取消、快照恢復通過 |
| integration / e2e | 真實 DB 可連線時通過 |
| race detector | 無 data race |
| smoke test | happy path 可跑通 |
| load / spike | 不出現明顯 500 或 panic |
| ws fanout | 可建立大量連線，無立即性崩潰 |

### 4.4 尚需補齊的本地 checklist

| 檢查項目 | 目前狀態 |
| :--- | :--- |
| 壓測後 correctness audit | 尚未實作 |
| S / M 級資料量固定測試資料 | 尚未實作 |
| 查詢 / snapshot restore 基準紀錄 | 尚未實作 |
| 本地測試報告模板 | 尚未實作 |

## 5. 本地資料量分層

| 級別 | 建議資料量 | 是否適合本地 | 用途 |
| :--- | :--- | :--- | :--- |
| S | `10^4` | ✅ 適合 | baseline、腳本驗證、happy path、基本 correctness |
| M | `10^6` | ✅ 大多適合 | 第一輪 query / index / snapshot restore 問題 |
| L | `10^7` | ⚠️ 視硬體而定 | 若本地資源夠，可做預檢；正式結論應在線上驗證 |
| XL | `10^8+` | ❌ 不建議 | 容量、資料成長與長時間風險應放在線上或專用環境 |

## 6. 本地階段應產出的結果

至少應產出：

1. 基本測試通過結果。
2. k6 baseline latency / error rate。
3. S / M 級資料量下的 query 與 snapshot restore 初步觀察。
4. 哪些問題已經在本地被排除，哪些需要上 ECS 才能驗證。

如果目前沒有辦法產出第 3 與第 4 項的標準化紀錄，代表本地測試體系仍屬於「baseline 可跑」，還不是「可穩定交付決策資訊」的階段。

## 7. 本地結論的使用方式

本地可以用來說：

1. 功能正確性已初步成立。
2. 腳本與測試流程可執行。
3. 小資料量與中資料量下沒有明顯結構性錯誤。

本地不應直接用來說：

1. 系統最終能扛多少人。
2. 系統能穩定承受多少 TPS。
3. 大資料量與多實例下會不會出現真實瓶頸。