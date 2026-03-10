# Git 工作流程規範

本文件定義了專案的 Git 管理規範，確保每個階段（Redis, Kafka, 微服務）都能獨立測試與演進。

---

## 🌳 分支策略：功能分支 (Feature Branches)

我們採用分支驅動開發，將重大技術組件拆分開發，確保 `main` 始終保持穩定。

### 分支路徑演進

```
main (Stage 1 Monolith)
  ├── feat/redis-cache      (階段 A: 加入 Redis 快取層)
  ├── feat/kafka-messaging  (階段 B: 加入 Kafka 非同步隊列)
  ├── feat/microservices    (階段 C: 拆分微服務與合併 A+B)
  └── feat/aws-ecs-deploy   (階段 D: Terraform + ecspresso 雲端部屬)
```

### 階段測試規範

| 分支 | 獨立測試行為 | 成功指標 |
| :--- | :--- | :--- |
| `feat/redis-cache` | 使用 `test-api-v1.sh` 測試回應延遲 | API 延遲顯著下降，且 Redis 斷線時系統不崩潰 |
| `feat/kafka-messaging` | 大量下單測試，觀察 API 回傳與後台處理時間差 | API 立即回傳 202，DB 異步完成處理 (削峰填谷) |
| `feat/microservices` | 重啟各微服務，驗證服務間通訊與重新連線機制 | 兩服務獨立運行下，撮合與成交流程依然正確 |
| `feat/aws-ecs-deploy` | 對 ALB Endpoint 進行壓力測試 | CloudWatch 觀察到正常的 CPU/Mem 分佈與 Scaling |

---

## 📝 Commit Message 規範 (Conventional Commits)

### 格式

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Type 類型

| Type       | 說明     | 範例                                                |
| ---------- | -------- | --------------------------------------------------- |
| `feat`     | 新功能   | `feat(matching): implement FIFO matching algorithm` |
| `fix`      | Bug 修復 | `fix(wallet): resolve race condition in lock funds` |
| `docs`     | 文件變更 | `docs(readme): add deployment instructions`         |
| `style`    | 格式調整 | `style(api): format code with gofmt`                |
| `refactor` | 重構     | `refactor(service): extract order validation logic` |
| `perf`     | 效能優化 | `perf(db): add index on orders.symbol`              |
| `test`     | 測試     | `test(service): add unit tests for PlaceOrder`      |
| `chore`    | 雜項     | `chore(deps): update go modules`                    |
| `ci`       | CI/CD    | `ci(github): add automated testing workflow`        |

### Scope 範圍

```
api       - HTTP API 層
core      - 核心業務邏輯
matching  - 撮合引擎
wallet    - 錢包服務
db        - 資料庫相關
infra     - 基礎設施
docker    - Docker 相關
```

### 範例

#### ✅ 好的 Commit Message

```
feat(matching): implement price-time priority matching algorithm

- Add OrderBook data structure using Red-Black Tree
- Implement FIFO matching for same price level
- Add unit tests with 95% coverage

Closes #123
```

```
fix(wallet): prevent race condition in concurrent fund locking

The previous implementation had a race condition when multiple
goroutines tried to lock funds simultaneously. This fix adds
a mutex to ensure atomic operations.

Fixes #456
```

#### ❌ 不好的 Commit Message

```
fix bug                          # 太籠統
update code                      # 沒說明做了什麼
WIP                             # 不應該提交 Work In Progress
Fixed typo in readme.md         # 應該用小寫開頭
```

---

## 🔄 工作流程

### 1. 開始新功能 (Start New Feature)

```bash
# 1. 確保在最新的 main 分支
git checkout main
git pull origin main

# 2. 建立新的功能分支
git checkout -b feature/matching-engine

# 3. 開始開發
# ... 寫程式碼 ...

# 4. 階段性提交 (小步快跑)
git add internal/core/matching/
git commit -m "feat(matching): add OrderBook data structure"

git add internal/core/matching/engine.go
git commit -m "feat(matching): implement basic matching logic"

# 5. 推送到遠端
git push origin feature/matching-engine
```

### 2. 提交 Pull Request (PR)

#### PR 標題格式

```
feat(matching): implement price-time priority matching engine
```

#### PR 描述模板

````markdown
## 📋 變更摘要

實作撮合引擎的核心邏輯，採用價格優先、時間優先的匹配演算法。

## 🎯 解決的問題

- 訂單無法自動撮合，需要手動處理
- 缺乏公平的匹配機制

## ✅ 完成項目

- [x] OrderBook 資料結構 (Red-Black Tree)
- [x] FIFO 匹配邏輯
- [x] 單元測試 (覆蓋率 95%)
- [x] 效能測試 (P99 < 10ms)

## 🧪 測試方法

```bash
go test ./internal/core/matching/... -v
```
````

## 📸 截圖/Demo

(如果有 UI 變更或效能圖表)

## 🔗 相關 Issue

Closes #123
Related to #456

## ⚠️ Breaking Changes

無

## 📚 文件更新

- [x] 更新 README.md
- [x] 更新 API 文件

````

### 3. Code Review 檢查清單

#### 作為 Author (PR 提交者)

- [ ] 程式碼符合專案規範 (執行 `go fmt`, `golangci-lint`)
- [ ] 所有測試通過
- [ ] 新增適當的單元測試
- [ ] 更新相關文件
- [ ] Commit message 符合規範
- [ ] PR 描述清晰完整

#### 作為 Reviewer (審核者)

- [ ] 程式邏輯正確
- [ ] 無明顯效能問題
- [ ] 錯誤處理完善
- [ ] 測試覆蓋關鍵路徑
- [ ] 無安全漏洞 (SQL Injection, XSS 等)
- [ ] 符合 SOLID 原則

### 4. 合併 (Merge)

```bash
# 方案 1: Squash and Merge (推薦)
# - 將多個 commits 合併成一個
# - 保持 main 分支簡潔

# 方案 2: Rebase and Merge
# - 保留所有 commit 歷史
# - 適合需要詳細歷史的情況

# 合併後刪除分支
git branch -d feature/matching-engine
git push origin --delete feature/matching-engine
````

---

## 🏷️ 版本號管理 (Semantic Versioning)

### 格式：`MAJOR.MINOR.PATCH`

```
v1.2.3
 │ │ │
 │ │ └─ PATCH: Bug 修復
 │ └─── MINOR: 新功能 (向後兼容)
 └───── MAJOR: 破壞性變更 (不向後兼容)
```

### 範例

```
v0.1.0  - 初始 MVP (Phase 1)
v0.2.0  - 加入撮合引擎
v0.3.0  - 加入微服務拆分
v1.0.0  - 第一個生產版本
v1.1.0  - 加入 Kafka 整合
v1.1.1  - 修復資金鎖定 Bug
v2.0.0  - API 重大變更
```

### 建立 Tag

```bash
# 1. 確保在 main 分支
git checkout main
git pull origin main

# 2. 建立 Tag (Annotated Tag)
git tag -a v0.1.0 -m "Release v0.1.0: Phase 1 MVP

- REST API for order placement
- PostgreSQL persistence
- Fund locking mechanism
"

# 3. 推送 Tag
git push origin v0.1.0

# 4. 查看所有 Tag
git tag -l
```

---

## 🔍 常用 Git 指令

### 檢視歷史

```bash
# 查看簡潔的 commit 歷史
git log --oneline --graph --all

# 查看某個檔案的變更歷史
git log -p internal/core/service.go

# 查看誰修改了某一行
git blame internal/core/service.go
```

### 修改 Commit

```bash
# 修改最後一次 commit message
git commit --amend

# 修改最後一次 commit 的內容 (加入遺漏的檔案)
git add forgotten_file.go
git commit --amend --no-edit

# 互動式 Rebase (修改歷史 commits)
git rebase -i HEAD~3
```

### 暫存工作

```bash
# 暫存目前的變更
git stash save "WIP: implementing matching logic"

# 查看暫存清單
git stash list

# 恢復暫存
git stash pop

# 刪除暫存
git stash drop stash@{0}
```

### 處理衝突

```bash
# 1. 拉取最新的 main
git checkout main
git pull origin main

# 2. 切回功能分支並 Rebase
git checkout feature/matching-engine
git rebase main

# 3. 如果有衝突，手動解決後
git add <resolved_files>
git rebase --continue

# 4. 強制推送 (因為歷史被改寫)
git push origin feature/matching-engine --force-with-lease
```

---

## 📂 .gitignore 配置

確保已建立 `.gitignore`：

```bash
# Binaries
*.exe
*.dll
*.so
*.dylib
server
*.out

# Test coverage
*.test
coverage.txt
coverage.html

# IDE
.vscode/
.idea/
*.swp
*.swo

# Environment
.env
.env.local

# Database
*.db
*.sqlite

# Logs
*.log
logs/

# Dependencies
vendor/

# OS
.DS_Store
Thumbs.db
```

---

## 🔒 敏感資訊處理

### ⚠️ 永遠不要提交

- ❌ `.env` 檔案 (環境變數)
- ❌ API Keys, Passwords
- ❌ AWS Credentials
- ❌ 私鑰 (Private Keys)

### ✅ 正確做法

```bash
# 1. 使用 .env.example 作為範本
cat > .env.example << EOF
DATABASE_URL=postgres://user:password@localhost:5432/dbname
REDIS_URL=redis://localhost:6379
AWS_ACCESS_KEY_ID=your_key_here
EOF

# 2. 在 README 中說明
echo "複製 .env.example 為 .env 並填入實際值" >> README.md

# 3. 確保 .env 在 .gitignore 中
echo ".env" >> .gitignore
```

### 🚨 如果不小心提交了敏感資訊

```bash
# 方案 1: 使用 git-filter-repo (推薦)
pip install git-filter-repo
git filter-repo --path .env --invert-paths

# 方案 2: 使用 BFG Repo-Cleaner
bfg --delete-files .env
git reflog expire --expire=now --all
git gc --prune=now --aggressive

# ⚠️ 注意：這些操作會改寫歷史，需要強制推送
git push origin --force --all
```

---

## 🎯 面試展示重點

### 1. 展示良好的 Commit 歷史

```bash
# 面試時展示
git log --oneline --graph --all --decorate

# 輸出範例：
# * a1b2c3d (HEAD -> main, tag: v0.1.0) feat(matching): implement matching engine
# * d4e5f6g fix(wallet): resolve race condition
# * g7h8i9j docs(readme): update deployment guide
# * j0k1l2m chore(deps): update dependencies
```

### 2. 展示 PR 流程

- 在 GitHub 上展示你的 PR
- 展示完整的 PR 描述
- 展示 Code Review 討論

### 3. 說明你的分支策略

```
面試官："你們團隊用什麼 Git 工作流？"

你的回答：
"我使用 GitHub Flow，因為它簡單且適合持續部署。
每個功能都在獨立的 feature 分支開發，
通過 Pull Request 進行 Code Review，
合併前確保 CI 通過且有至少一位 Reviewer 批准。

我遵循 Conventional Commits 規範撰寫 commit message，
方便自動生成 Changelog 和追蹤變更歷史。"
```

---

## 🚀 初始化專案 Git (從零開始)

```bash
# 1. 初始化 Git
cd /Volumes/KINGSTON/Programming/Go/exchange
git init

# 2. 建立 .gitignore
curl -o .gitignore https://raw.githubusercontent.com/github/gitignore/main/Go.gitignore

# 3. 首次 Commit
git add .
git commit -m "chore: initial project setup

- Initialize Go module
- Setup project structure
- Add Docker Compose for PostgreSQL and Redis
- Create basic API server
"

# 4. 建立遠端倉庫 (在 GitHub 上)
# 然後連結
git remote add origin git@github.com:RayLiu1999/exchange.git
git branch -M main
git push -u origin main

# 5. 建立第一個 Tag
git tag -a v0.0.1 -m "Initial commit"
git push origin v0.0.1
```

---

## 📚 推薦學習資源

- [Pro Git Book](https://git-scm.com/book/zh-tw/v2) - Git 官方教學
- [Conventional Commits](https://www.conventionalcommits.org/) - Commit 訊息規範
- [GitHub Flow](https://guides.github.com/introduction/flow/) - GitHub 工作流
- [Semantic Versioning](https://semver.org/) - 版本號規範

---

## ✅ Checklist：良好的 Git 習慣

- [ ] 每天至少 commit 一次 (小步快跑)
- [ ] Commit message 符合 Conventional Commits
- [ ] 功能完成後立即提交 PR
- [ ] PR 描述清晰完整
- [ ] 定期 Rebase main 分支保持同步
- [ ] 合併後刪除 feature 分支
- [ ] 重要版本建立 Git Tag
- [ ] 不提交敏感資訊
- [ ] Code Review 認真檢查
- [ ] CI 通過才能合併
