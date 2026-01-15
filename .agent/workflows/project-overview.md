---
description: 專案總覽與開發規範
---

# Exchange 專案工作流程規範

本文件是 AI 助手在此專案中遵循的主要規範文件。

## 專案概述

**名稱**：Crypto Exchange (加密貨幣交易所)
**語言**：Go 1.21+
**目標**：學習分散式系統設計，展示面試時的技術能力

## 專案目錄結構

```
exchange/
├── api/                    # Protobuf/gRPC 定義
├── cmd/                    # 應用程式入口
│   ├── server/            # 主要 HTTP Server (Phase 1)
│   ├── gateway/           # (Phase 2) API Gateway
│   ├── matching-engine/   # (Phase 2) 撮合引擎
│   └── order-service/     # (Phase 2) 訂單服務
├── internal/              # 私有程式碼
│   ├── core/             # 核心業務邏輯 (Domain)
│   ├── repository/       # 資料存取層
│   ├── api/              # HTTP API 層
│   └── infrastructure/   # Kafka, Redis 等
├── sql/                   # SQL Schema
├── deploy/                # Kubernetes/Docker 部署
├── terraform/             # AWS IaC
└── docs/                  # 專案文件
```

## 關鍵文件索引

| 文件 | 用途 |
|-----|-----|
| `docs/PROJECT_PLAN.md` | 階段性開發計劃與面試重點 |
| `docs/ARCHITECTURE.md` | 分層架構設計說明 |
| `docs/SYSTEM_DESIGN.md` | 生產環境系統架構 (EKS) |
| `docs/GIT_WORKFLOW.md` | Git 分支與 Commit 規範 |
| `docs/AWS_DEPLOYMENT_GUIDE.md` | AWS 部署完整指南 |
| `.github/copilot-instructions.md` | AI 助手行為規範 |

## 開發規範 (必須遵守)

1. **語言**：繁體中文撰寫註解與 log
2. **Go import**：確保每個檔案只引入必要的 package，避免重複
3. **Markdown**：使用標準語法，含標題、清單、程式碼區塊
4. **技術聚焦**：文件內容聚焦技術細節與架構設計

## Makefile 常用指令

```bash
make dev          # 開發模式：啟動 DB + Migration + Server
make build        # 編譯專案
make run          # 啟動伺服器
make test         # 執行測試
make lint         # 程式碼檢查
make db-up        # 啟動 PostgreSQL (Docker)
make db-migrate   # 執行 Schema Migration
make db-reset     # 重置資料庫
```

## Git Commit 規範

使用 Conventional Commits：

```
<type>(<scope>): <subject>

Types: feat, fix, docs, style, refactor, perf, test, chore, ci
Scopes: api, core, matching, wallet, db, infra, docker
```

## 當前開發階段

專案分為 4 個 Phase：

- **Phase 1**: 單體 MVP ← 目前所在位置
- **Phase 2**: 微服務拆分
- **Phase 3**: 非同步與事件驅動 (Kafka, Saga)
- **Phase 4**: 雲端部署與監控 (AWS EKS)

## 開發方式：TDD (測試驅動開發)

本專案採用 **TDD TODO List** 方法進行開發：

1. **寫下 TODO List** - 列出所有預期行為
2. **選最簡單的 TODO** - 寫一個會失敗的測試 (Red)
3. **最少程式碼通過測試** (Green)
4. **重構** (Refactor)
5. **發現細節，更新 TODO List**
6. **重複直到完成**

詳細流程請參考 `/tdd-development` workflow。
