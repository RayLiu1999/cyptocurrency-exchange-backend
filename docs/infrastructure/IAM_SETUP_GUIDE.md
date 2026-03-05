# AWS IAM 設定指南

> **你的正確認知**：Root 帳號只做一件事：管理 IAM。其他所有操作（開 EC2、Push Image、設定預算）都用 IAM User 完成。Root 帳號的 Access Key **永遠不要建立**。

---

## 目錄

- [Step 1：Root 帳號安全加固](#step-1root-帳號安全加固)
- [Step 2：建立 IAM User（你的操作帳號）](#step-2建立-iam-user)
- [Step 3：設定權限（這個專案需要哪些）](#step-3設定權限)
- [Step 4：設定預算警報](#step-4設定預算警報)
- [Step 5：本機設定 AWS CLI](#step-5本機設定-aws-cli)
- [權限說明：為什麼需要每一條](#權限說明)

---

## Step 1：Root 帳號安全加固

登入 AWS Console（用你的 email + 密碼，這是 Root）。

### 1-1. 開啟 Root MFA

```
右上角帳號名稱 → Security credentials
→ Multi-factor authentication (MFA)
→ Assign MFA device
→ 選 Authenticator app（用 Google Authenticator 或 1Password）
→ 掃 QR Code，輸入兩組 6 位數 → Add MFA
```

**完成後，Root 登入需要 email + 密碼 + MFA，非常安全。**

### 1-2. 確認 Root 沒有 Access Key

```
同一個頁面 → Access keys
→ 應該顯示「No access keys」
→ 如果有，立刻 Delete
```

Root 的 Access Key 一旦洩漏，整個 AWS 帳號就沒了。

### 1-3. 設定帳單警報的前置條件（只有 Root 能做）

```
右上角帳號名稱 → Account
→ IAM user and role access to Billing information
→ 點 Edit → 勾選 Activate IAM Access → Update
```

**這步很重要**，不做的話 IAM User 就算有 Billing 權限也看不到帳單。

---

## Step 2：建立 IAM User

**從這裡開始就不需要 Root，但因為要建立 IAM，所以這次還是 Root 操作。之後 Root 就可以收起來了。**

### 2-1. 建立 IAM User

```
搜尋 IAM → Users → Create user

User name: exchange-dev        ← 自己取名，建議跟專案掛鉤
☑ Provide user access to the AWS Management Console
→ 選 I want to create an IAM user
→ 設定 Console password（建議用密碼管理器產生）
→ ☑ Users must create a new password at next sign-in（可以取消，自己用比較方便）

Next → （先跳過 Permissions，Step 3 再設定）
Next → Create user
```

### 2-2. 建立 Access Key（給 CLI / Terraform 用）

```
點進剛建立的 exchange-dev user
→ Security credentials
→ Access keys → Create access key
→ 選 Command Line Interface (CLI)
→ 勾選同意 → Next → Create access key

⚠️  這是唯一能看到 Secret Access Key 的時機，立刻存進密碼管理器。
    .csv 也下載一份備用。
```

### 2-3. 幫 IAM User 開啟 MFA

```
同一個頁面 → Multi-factor authentication (MFA)
→ Assign MFA device → 設定方式同 Root
```

---

## Step 3：設定權限

**這個專案實際會用到的 AWS 服務：**

| 階段 | 用到的服務 |
|---|---|
| Phase 1（EC2 + Docker）| EC2, ECR, VPC, Security Groups |
| Phase 4（ECS）| ECS, ELB（ALB）, IAM（給 ECS Task 用）|
| Phase 6（監控）| CloudWatch |
| 全程 | Budgets, Cost Explorer（看帳單）|

### 3-1. 附加 AWS 託管 Policy（快速、夠用）

```
IAM → Users → exchange-dev
→ Permissions → Add permissions → Attach policies directly
```

搜尋並勾選以下 Policy：

```
✅ AmazonEC2FullAccess
   → 開/關 EC2、設定 Security Group、管理 Key Pair

✅ AmazonEC2ContainerRegistryFullAccess
   → Push/Pull Docker Image 到 ECR

✅ AmazonECS_FullAccess
   → 建立 ECS Cluster、Task Definition、Service（Phase 4 用）

✅ ElasticLoadBalancingFullAccess
   → 建立 ALB、Target Group（Phase 4 用）

✅ CloudWatchFullAccess
   → 看 EC2 metrics、ECS logs、設定 Alarm

✅ AWSBudgetsFullAccess
   → 建立預算警報，超過設定金額時通知你

✅ ReadOnlyAccess  （可選，按需求）
   → 純讀取所有服務，方便查看資源狀態
```

> 點 **Add permissions** 完成。

### 3-2. 補一個 Cost Explorer 的 Inline Policy

AWS 託管 Policy 裡沒有完整的 Cost Explorer 讀取權限，手動補一個：

```
IAM → Users → exchange-dev
→ Permissions → Add permissions → Create inline policy
→ 切換到 JSON，貼入以下內容：
```

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CostExplorerReadAccess",
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage",
        "ce:GetCostForecast",
        "ce:GetUsageForecast",
        "ce:GetDimensionValues",
        "ce:GetReservationUtilization",
        "ce:ListCostAllocationTags"
      ],
      "Resource": "*"
    },
    {
      "Sid": "IAMPassRoleForECS",
      "Effect": "Allow",
      "Action": [
        "iam:PassRole",
        "iam:GetRole",
        "iam:CreateRole",
        "iam:AttachRolePolicy",
        "iam:PutRolePolicy"
      ],
      "Resource": "arn:aws:iam::*:role/exchange-*",
      "Condition": {
        "StringLike": {
          "iam:PassedToService": "ecs-tasks.amazonaws.com"
        }
      }
    }
  ]
}
```

```
→ Policy name: exchange-dev-extra
→ Create policy
```

> **`iam:PassRole` 說明**：ECS Task 需要一個 IAM Role 才能拉 ECR image 和寫 CloudWatch log。這條權限讓你能把 Role 「交給」ECS Task，但限定只能跨給 `exchange-*` 開頭的 Role，避免權限擴散。

### 3-3. 確認最終的 Permissions 樣子

設定完後，`exchange-dev` 的 Permissions 應該長這樣：

```
Permissions policies (7)
├── AmazonEC2FullAccess                        (AWS managed)
├── AmazonEC2ContainerRegistryFullAccess       (AWS managed)
├── AmazonECS_FullAccess                       (AWS managed)
├── ElasticLoadBalancingFullAccess             (AWS managed)
├── CloudWatchFullAccess                       (AWS managed)
├── AWSBudgetsFullAccess                       (AWS managed)
└── exchange-dev-extra                         (Inline)
```

---

## Step 4：設定預算警報

**用 IAM User 登入後操作（Root 已經開了 Billing 存取）：**

```
搜尋 Budgets → Create budget
→ 選 Use a template → Monthly cost budget

Budget name: exchange-monthly-limit
Budgeted amount ($): 50          ← 設你能接受的上限

Email recipients: your@email.com

→ Create budget
```

AWS 預設會在達到 85% 和 100% 時寄 email 通知。

**另外設一個異常偵測：**

```
Budgets → Budget Actions 或 Cost Anomaly Detection
→ Create monitor
→ Monitor type: AWS services
→ 選 EC2、ECS、RDS
→ Alerting threshold: $10（單日突然多 $10 就通知）
→ 填 email → Create monitor
```

> 這個功能可以抓到「忘記 destroy，EC2 跑了一個禮拜」這種意外。

---

## Step 5：本機設定 AWS CLI

**從這裡開始，Root 帳號就不需要再動了。**

```bash
# 用你剛才下載的 Access Key 設定
aws configure --profile exchange-dev

# AWS Access Key ID: AKIA...（你的 Access Key）
# AWS Secret Access Key: ...（你的 Secret Key）
# Default region name: ap-northeast-1
# Default output format: json
```

**設定完後，驗證身份：**

```bash
aws sts get-caller-identity --profile exchange-dev
```

回傳結果應該像這樣：

```json
{
    "UserId": "AIDA...",
    "Account": "123456789012",
    "Arn": "arn:aws:iam::123456789012:user/exchange-dev"
}
```

**設定 default profile，省掉每次輸入 `--profile`：**

```bash
export AWS_PROFILE=exchange-dev

# 或加到 ~/.zshrc 讓它永久生效
echo 'export AWS_PROFILE=exchange-dev' >> ~/.zshrc
```

---

## Step 6：建立 ECS Task 需要的 IAM Role（Terraform 前置）

ECS Fargate 的 Task 需要兩個 Role：

| Role | 作用 |
|---|---|
| **Task Execution Role** | ECS 拉 ECR image、寫 CloudWatch log（ECS agent 用）|
| **Task Role** | 你的 Go 程式運行時，如果需要呼叫 AWS API（如 S3）才需要 |

這個專案目前 Task Role 不需要（Go 程式只連 DB/Redis，不呼叫 AWS API）。只需要建立 Task Execution Role。

**用 CLI 建立（IAM User 身份）：**

```bash
# 建立 Role（信任 ECS tasks）
aws iam create-role \
  --role-name exchange-ecs-task-execution-role \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": { "Service": "ecs-tasks.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }]
  }'

# 附加 AWS 託管的 ECS 執行 Policy
aws iam attach-role-policy \
  --role-name exchange-ecs-task-execution-role \
  --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy

# 確認建立成功
aws iam get-role --role-name exchange-ecs-task-execution-role
```

在之後的 Terraform `ecs.tf` 裡，`execution_role_arn` 就填這個 Role 的 ARN。

---

## 權限說明

### 為什麼不直接用 `AdministratorAccess`？

`AdministratorAccess` 等同於 Root 的完整權限。Access Key 一旦洩漏（例如不小心 commit 進 git），攻擊者可以做任何事：開礦機、刪資料、轉帳。

最小權限原則的目的：**洩漏了，頂多只能動這個專案用到的服務。**

### 每條 Policy 的作用速查

```
AmazonEC2FullAccess
├── 開/關 Instance
├── 建立/修改 Security Group
├── 管理 Key Pair
├── 建立 VPC、Subnet、Route Table
└── 建立 Elastic IP

AmazonEC2ContainerRegistryFullAccess
├── docker push（本機 → ECR）
├── docker pull（EC2/ECS → ECR）
└── 建立/刪除 Repository

AmazonECS_FullAccess
├── 建立 Cluster
├── 建立 Task Definition
├── 建立/更新 Service
└── 查看 Task 狀態與 Log

ElasticLoadBalancingFullAccess
├── 建立 ALB
├── 建立 Target Group
├── 設定 Listener 和 Rules
└── 查看連線狀態

CloudWatchFullAccess
├── 查看 EC2/ECS metrics
├── 查看 Container logs（`/ecs/exchange-backend`）
├── 建立 Dashboard
└── 建立 Alarm（CPU > 80% 時通知）

AWSBudgetsFullAccess
├── 建立月度預算
├── 設定通知 email
└── 查看當月消費

exchange-dev-extra (Inline)
├── Cost Explorer：查看帳單明細和預測
└── iam:PassRole：把 ECS Execution Role 交給 ECS Task（必要）
```

### 以後想分測試/生產環境怎麼做？

不需要建多個 IAM User，而是用 **AWS Organizations + 多帳號**：

```
Root 帳號（Management Account）
├── 開發帳號（123456789001）← exchange-dev IAM user 在這
├── 測試帳號（123456789002）← 壓測環境
└── 生產帳號（123456789003）← 以後真的要上線才開
```

每個帳號的費用獨立計算，權限完全隔離。這是大公司的標準做法，但現階段學習用不需要，知道有這個東西存在就好。

---

## 快速檢查清單

完成後確認以下項目：

```
Root 帳號
  ☑ MFA 已開啟
  ☑ 沒有 Access Key
  ☑ 已開啟 IAM Billing Access

IAM User (exchange-dev)
  ☑ MFA 已開啟
  ☑ Access Key 已存進密碼管理器
  ☑ 7 條 Policy 都附加了

預算
  ☑ Monthly budget $50 已建立
  ☑ Cost Anomaly Detection 已設定

本機
  ☑ aws configure --profile exchange-dev 設定完成
  ☑ aws sts get-caller-identity 回傳正確的 user ARN
  ☑ AWS_PROFILE=exchange-dev 已加到 ~/.zshrc

ECS 前置
  ☑ exchange-ecs-task-execution-role 已建立
```
