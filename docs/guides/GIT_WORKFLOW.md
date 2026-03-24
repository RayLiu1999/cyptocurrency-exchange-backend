# Git 工作流程規範

## 分支策略

採用 GitHub Flow，以 `main` 為穩定基線，功能開發皆在 Feature Branch 完成後透過 PR 合併。

```
main (穩定版本)
  ├── feat/<feature-name>
  ├── fix/<bug-description>
  └── chore/<task-description>
```

---

## Commit Message 規範 (Conventional Commits)

格式：`<type>(<scope>): <subject>`

| Type | 說明 | 範例 |
|:--|:--|:--|
| `feat` | 新功能 | `feat(matching): implement FIFO matching` |
| `fix` | Bug 修復 | `fix(wallet): resolve race condition` |
| `refactor` | 重構 | `refactor(service): extract validation logic` |
| `perf` | 效能優化 | `perf(db): add index on orders.symbol` |
| `test` | 測試 | `test(service): add unit tests for PlaceOrder` |
| `docs` | 文件 | `docs(readme): add deployment instructions` |
| `chore` | 雜項 | `chore(deps): update go modules` |
| `ci` | CI/CD | `ci(github): add testing workflow` |

**Scope 範圍**：`api` / `core` / `matching` / `wallet` / `db` / `infra` / `docker`

---

## PR 流程

1. 從最新 `main` 建立 Feature Branch
2. 開發完成後推送並建立 PR
3. PR 標題遵循 Commit 規範格式
4. 確保 CI 通過後合併（推薦 Squash and Merge）
5. 合併後刪除 Feature Branch

### PR 描述必填項目

- 變更摘要
- 測試方法（如何驗證）
- Breaking Changes（如有）

---

## 版本號 (Semantic Versioning)

格式：`vMAJOR.MINOR.PATCH`

```bash
git tag -a v0.1.0 -m "Release v0.1.0: Phase 1 MVP"
git push origin v0.1.0
```
