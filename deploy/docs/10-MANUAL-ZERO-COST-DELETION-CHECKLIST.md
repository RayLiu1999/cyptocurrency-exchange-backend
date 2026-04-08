# 10 — 手動刪除至零費用清單

本文件提供一份「Terraform state 已經漂移，或你已手動刪過部分 AWS 資源」時可直接照著做的手動清除清單。

目標不是只把 staging 看起來刪乾淨，而是盡可能把**所有仍可能持續產生費用的資源**都確認清掉。

> 重要：AWS Billing / Cost Explorer 可能會延遲數小時到 24 小時才反映最新費用。只要本文件中的高風險資源都已不存在，就代表**不應再持續新增新的費用**。

## 使用時機

適用以下情境：

| 情境 | 說明 |
|------|------|
| Terraform `destroy` 顯示成功，但你懷疑 AWS 還有殘留資源 | 代表 state 與實際雲端資源可能已脫節 |
| 你曾在 AWS Console 手動刪除部分資源 | 之後不能再完全信任 Terraform state |
| 你現在的唯一目標是「不要再產生任何費用」 | 優先看本文件，不要先糾結 state 是否漂亮 |

## 結論先看：哪些東西一定要刪

下表依「是否會持續產生費用」整理：

| 類別 | 資源 | 是否一定要刪 | 原因 |
|------|------|----------------|------|
| 計算 | ECS Services / Tasks | 是 | Fargate task 會持續計費 |
| 流量入口 | ALB / Target Group | 是 | ALB 即使沒有流量也會持續計費 |
| 網路 | NAT Gateway | 是 | NAT Gateway 是最常見且最高的漏費來源 |
| 網路 | Unattached Elastic IP | 是 | 未綁定的 EIP 仍會計費 |
| 資料庫 | RDS PostgreSQL instance | 是 | 只要 instance 存在就會持續計費 |
| 資料庫 | RDS manual snapshots | 是 | snapshot 會佔用儲存空間並持續計費 |
| 快取 | ElastiCache replication group / cache cluster | 是 | Redis 節點會持續計費 |
| 記錄與映像 | CloudWatch log groups | 建議刪 | 儲存量雖小，但若追求歸零應刪除 |
| 記錄與映像 | ECR repositories / images | 建議刪 | 儲存量雖小，但不是零費用 |
| 服務發現 | Cloud Map services / namespace | 建議刪 | private DNS namespace 會連動 Route 53 private hosted zone |
| Terraform backend | S3 state bucket | 若不再使用 staging，應刪 | S3 儲存不是零費用 |
| Terraform backend | DynamoDB lock table | 若不再使用 staging，應刪 | 儲存雖小，但不是絕對零 |
| 舊部署殘留 | EFS file systems / mount targets | 若存在，應刪 | 歷史 Redpanda + EFS 方案可能留下費用 |

## 哪些通常不用刪也不會直接產生費用

| 資源 | 是否可保留 | 說明 |
|------|------------|------|
| ECS task definitions | 可保留 | 不直接計費 |
| IAM roles / policies | 可保留 | 不直接計費 |
| Security Groups | 可保留 | 不直接計費 |
| Route Tables / NACL / Internet Gateway | 可保留 | 不直接計費，但通常會和 VPC 一起清掉 |
| 空的 ECS Cluster | 可保留 | 叢集本身不計費，但建議刪除以免混淆 |
| Standard SSM Parameters | 可保留 | 一般不構成主要費用來源 |

> 若你的目標是「帳單趨近 0 且之後不再重建 staging」，建議連這些免費資源也一併清理，讓環境回到真正乾淨的狀態。

## 手動刪除順序

手動刪除時，建議照這個順序做，避免依賴關係互卡：

| 順序 | 先刪什麼 | 原因 | AWS Console 路徑 |
|------|----------|------|------------------|
| 1 | ECS Services / Tasks | 先停掉計算費用，並解除後續網路與 service discovery 依賴 | ECS -> Clusters -> `exchange-staging` -> Services |
| 2 | ALB / Listeners / Target Groups | 先移除對外入口，避免 VPC 與 SG 被卡住 | EC2 -> Load Balancers / Target Groups |
| 3 | NAT Gateway / Elastic IP | 最高費用來源之一，應優先確認 | VPC -> NAT Gateways / Elastic IPs |
| 4 | RDS / Snapshots | DB instance 與 snapshot 都會持續計費 | RDS -> Databases / Snapshots |
| 5 | ElastiCache Redis | Redis 節點持續計費 | ElastiCache -> Replication groups |
| 6 | EFS 舊殘留 | 舊版 Redpanda + EFS 可能還在 | EFS -> File systems |
| 7 | ECR / CloudWatch Logs | 儲存成本不高，但要歸零就應清乾淨 | ECR / CloudWatch |
| 8 | Cloud Map / Route 53 private hosted zone | private DNS namespace 可能留下月費 | Cloud Map / Route 53 |
| 9 | S3 state bucket / DynamoDB lock table | 若短期不會再部署 staging，才刪 | S3 / DynamoDB |

## Step 1：刪除 ECS Services 與 Tasks

### 必須確認的服務名稱

目前或歷史可能存在的服務包含：

| 類型 | 服務名稱 |
|------|----------|
| 核心微服務 | `gateway`、`order-service`、`matching-engine`、`market-data-service` |
| 可選服務 | `simulation-service` |
| 訊息服務 | `redpanda` |
| 舊部署 | `monolith` |

### 刪除前檢查

```bash
AWS_PAGER=''

aws ecs list-clusters \
  --region ap-northeast-1 \
  --query 'clusterArns[?contains(@, `exchange`)]' \
  --output text
```

若 `exchange-staging` 還存在，繼續檢查 services：

```bash
AWS_PAGER=''
CLUSTER=exchange-staging

aws ecs list-services \
  --cluster "$CLUSTER" \
  --region ap-northeast-1 \
  --output text
```

### 手動刪除要求

- 將 service 的 `desired count` 調成 `0`。
- 等 running tasks 歸零後，再刪除 service。
- 若 Console 顯示 service 已不存在，視為已清除。

### 驗證結果應為空

```bash
AWS_PAGER=''
CLUSTER=exchange-staging

aws ecs list-services \
  --cluster "$CLUSTER" \
  --region ap-northeast-1 \
  --output text

aws ecs list-tasks \
  --cluster "$CLUSTER" \
  --region ap-northeast-1 \
  --output text
```

## Step 2：刪除 ALB、Listeners 與 Target Groups

### 需要清掉的項目

| 資源 | 原因 |
|------|------|
| Application Load Balancer | 持續計費 |
| Listener | 會隨 ALB 一起消失，但需確認 |
| Target Group | 可能在 ALB 刪掉後仍殘留 |

### 驗證指令

```bash
AWS_PAGER=''

aws elbv2 describe-load-balancers \
  --region ap-northeast-1 \
  --query 'LoadBalancers[?contains(LoadBalancerName, `exchange`)].LoadBalancerName' \
  --output text

aws elbv2 describe-target-groups \
  --region ap-northeast-1 \
  --query 'TargetGroups[?contains(TargetGroupName, `exchange`)].TargetGroupName' \
  --output text
```

兩個查詢都應為空。

## Step 3：刪除 NAT Gateway 與 Elastic IP

這是最重要的費用風險點。

### 為什麼 NAT 要獨立確認

| 原因 | 說明 |
|------|------|
| 單價高 | staging 中最容易持續漏費 |
| 刪除較慢 | Console 看起來像刪了，但實際會經過 `deleting` -> `deleted` |
| 常伴隨 EIP 殘留 | NAT 刪掉後若 EIP 沒釋放，仍可能計費 |

### 驗證指令

```bash
AWS_PAGER=''

aws ec2 describe-nat-gateways \
  --region ap-northeast-1 \
  --filter 'Name=tag:Project,Values=exchange' \
  --query 'NatGateways[*].{Id:NatGatewayId,State:State}' \
  --output table
```

可接受結果：

- 沒有任何列。
- 或全部都是 `deleted`。

再確認 Elastic IP：

```bash
AWS_PAGER=''

aws ec2 describe-addresses \
  --region ap-northeast-1 \
  --query 'Addresses[?AssociationId==null].[AllocationId,PublicIp]' \
  --output table
```

若這裡有你 staging 用過的 EIP，請到 Console 手動 `Release Elastic IP address`。

## Step 4：刪除 RDS instance 與 snapshots

### 必刪項目

| 資源 | 說明 |
|------|------|
| DB instance | 主要資料庫費用來源 |
| Manual snapshots | 雖然 instance 已刪，但 snapshot 仍會持續收費 |
| Automated backups | 刪 instance 時建議同步清掉 |

### 驗證指令

```bash
AWS_PAGER=''

aws rds describe-db-instances \
  --region ap-northeast-1 \
  --query 'DBInstances[?contains(DBInstanceIdentifier, `exchange`)].DBInstanceIdentifier' \
  --output text

aws rds describe-db-snapshots \
  --region ap-northeast-1 \
  --query 'DBSnapshots[?contains(DBSnapshotIdentifier, `exchange`)].DBSnapshotIdentifier' \
  --output text
```

兩者都應為空。

## Step 5：刪除 ElastiCache Redis

### 驗證指令

```bash
AWS_PAGER=''

aws elasticache describe-replication-groups \
  --region ap-northeast-1 \
  --query 'ReplicationGroups[?contains(ReplicationGroupId, `exchange`)].ReplicationGroupId' \
  --output text

aws elasticache describe-cache-clusters \
  --region ap-northeast-1 \
  --query 'CacheClusters[?contains(CacheClusterId, `exchange`)].CacheClusterId' \
  --output text
```

兩者都應為空。

## Step 6：刪除 EFS 舊殘留

> 現行 staging 已不再把 Redpanda 掛在 EFS 上，但舊版部署或中途失敗的實驗，仍可能留下 EFS 與 mount targets。

### 驗證指令

```bash
AWS_PAGER=''

aws efs describe-file-systems \
  --region ap-northeast-1 \
  --output table
```

若仍有與 `exchange` / `redpanda` 有關的 file system：

1. 先刪 mount target。
2. 再刪 file system。

## Step 7：刪除 ECR 與 CloudWatch Logs

這兩者通常不是高費用來源，但如果你要「完全不再產生任何費用」，應一併清掉。

### ECR 驗證

```bash
AWS_PAGER=''

aws ecr describe-repositories \
  --region ap-northeast-1 \
  --query 'repositories[?contains(repositoryName, `exchange`)].repositoryName' \
  --output text
```

### CloudWatch Logs 驗證

```bash
AWS_PAGER=''

aws logs describe-log-groups \
  --region ap-northeast-1 \
  --log-group-name-prefix '/ecs/exchange' \
  --query 'logGroups[*].logGroupName' \
  --output text
```

兩者都應為空。

## Step 8：刪除 Cloud Map namespace 與 Route 53 private hosted zone

### 要注意的點

| 資源 | 為何要查 |
|------|----------|
| Cloud Map services | 可能因 state drift 沒有被 Terraform 清掉 |
| Cloud Map namespace | private DNS namespace 會對應 Route 53 private hosted zone |
| Route 53 hosted zone | 如果 namespace 先被手動拆壞，hosted zone 可能殘留 |

### 驗證指令

```bash
AWS_PAGER=''

aws servicediscovery list-namespaces \
  --region ap-northeast-1 \
  --query 'Namespaces[?contains(Name, `exchange.internal`)]' \
  --output json

aws servicediscovery list-services \
  --region ap-northeast-1 \
  --query 'Services[?contains(Name, `gateway`) || contains(Name, `order-service`) || contains(Name, `matching-engine`) || contains(Name, `market-data-service`)]' \
  --output json
```

若你懷疑 Cloud Map 已被手動破壞，但 Route 53 還有殘留，再看：

```bash
AWS_PAGER=''

aws route53 list-hosted-zones-by-name \
  --dns-name exchange.internal \
  --output json
```

## Step 9：刪除 Terraform backend 的 S3 bucket 與 DynamoDB lock table

> 只有在你短期內**不打算再重建 staging** 時，才建議刪除這兩項。若還會再部署，可保留，但那就不是「絕對零費用」。

### 需要刪除的項目

| 資源 | 目前預設名稱 |
|------|--------------|
| S3 state bucket | `exchange-terraform-state-bucket` |
| DynamoDB lock table | `exchange-terraform-locks` |

### 驗證指令

```bash
AWS_PAGER=''

aws s3 ls | grep exchange-terraform-state || true

aws dynamodb list-tables \
  --region ap-northeast-1 \
  --query 'TableNames[?contains(@, `exchange-terraform-locks`)]' \
  --output text
```

## 一次驗證是否還有費用風險

以下查詢若全部為空，或 NAT 只剩 `deleted` 狀態，代表主要費用來源已移除：

```bash
AWS_PAGER=''

aws ecs list-clusters --region ap-northeast-1 --query 'clusterArns[?contains(@, `exchange`)]' --output text
aws elbv2 describe-load-balancers --region ap-northeast-1 --query 'LoadBalancers[?contains(LoadBalancerName, `exchange`)].LoadBalancerName' --output text
aws ec2 describe-nat-gateways --region ap-northeast-1 --filter 'Name=tag:Project,Values=exchange' --query 'NatGateways[*].{Id:NatGatewayId,State:State}' --output table
aws rds describe-db-instances --region ap-northeast-1 --query 'DBInstances[?contains(DBInstanceIdentifier, `exchange`)].DBInstanceIdentifier' --output text
aws rds describe-db-snapshots --region ap-northeast-1 --query 'DBSnapshots[?contains(DBSnapshotIdentifier, `exchange`)].DBSnapshotIdentifier' --output text
aws elasticache describe-replication-groups --region ap-northeast-1 --query 'ReplicationGroups[?contains(ReplicationGroupId, `exchange`)].ReplicationGroupId' --output text
aws ecr describe-repositories --region ap-northeast-1 --query 'repositories[?contains(repositoryName, `exchange`)].repositoryName' --output text
aws logs describe-log-groups --region ap-northeast-1 --log-group-name-prefix '/ecs/exchange' --query 'logGroups[*].logGroupName' --output text
aws servicediscovery list-namespaces --region ap-northeast-1 --query 'Namespaces[?contains(Name, `exchange.internal`)]' --output json
aws dynamodb list-tables --region ap-northeast-1 --query 'TableNames[?contains(@, `exchange-terraform-locks`)]' --output text
```

## 常見誤判

| 現象 | 真正含義 |
|------|----------|
| `terraform destroy` 顯示成功，但 AWS 仍有資源 | 代表 state 已空，但不代表 AWS 真正全清乾淨 |
| NAT Gateway 查詢還看得到一筆 | 若狀態是 `deleted`，通常可接受；若是 `available` / `deleting`，仍要追蹤 |
| ECR / Logs 還留著 | 雖然費用低，但不是零 |
| Cost Explorer 還有前幾小時的費用 | 這是計費報表延遲，不代表資源還在持續產生成本 |

## 最後建議

若你已經手動刪過一部分 AWS 資源，**不要再只依賴 Terraform state 當作真相來源**。此時最可靠的方式是：

1. 以 AWS Console / AWS CLI 逐類型檢查 billable resources 是否仍存在。
2. 先確保高風險費用資源全部歸零。
3. 最後再決定要不要補做 Terraform state 清理或重建。

若你的唯一目標是「今天開始不再繼續花錢」，本文件的檢查表比 `terraform destroy` 的成功訊息更重要。