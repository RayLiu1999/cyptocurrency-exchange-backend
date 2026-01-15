---
description: 開發新功能的完整流程
---

# 開發新功能流程

## 1. 建立功能分支

```bash
git checkout main
git pull origin main
git checkout -b feature/<功能名稱>
```

分支命名範例：
- `feature/matching-engine`
- `feature/kafka-integration`
- `bugfix/fix-race-condition`

## 2. 開發功能

### 檔案建立順序

1. **Domain 模型** (`internal/core/domain.go`)
2. **介面定義** (`internal/core/ports.go`)
3. **業務邏輯** (`internal/core/service.go`)
4. **資料存取** (`internal/repository/`)
5. **HTTP API** (`internal/api/handlers.go`)
6. **單元測試** (`*_test.go`)

### 開發時持續測試

```bash
make test
make lint
```

## 3. 提交程式碼

使用 Conventional Commits 格式：

```bash
git add <files>
git commit -m "feat(matching): implement price-time priority matching"
```

常用 type：
- `feat`: 新功能
- `fix`: Bug 修復
- `refactor`: 重構
- `test`: 測試
- `docs`: 文件

## 4. 推送並建立 PR

```bash
git push origin feature/<功能名稱>
```

在 GitHub 上建立 Pull Request，標題格式：

```
feat(matching): implement price-time priority matching engine
```

## 5. Code Review Checklist

- [ ] 程式碼符合 `make lint` 檢查
- [ ] 所有測試通過 `make test`
- [ ] 有適當的單元測試
- [ ] 繁體中文註解
- [ ] 更新相關文件

## 6. 合併

PR 審核通過後，使用 **Squash and Merge** 合併。
