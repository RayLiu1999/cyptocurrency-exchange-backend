# 03 — 完整資源清除指南 (Teardown)

> ⚠️ **重要**：此文件說明如何**完全移除**所有 AWS 資源，確保不被默默計費。  
> 按照本文件步驟操作後，你的 AWS 帳單應該歸零。

---

## 了解你建立了哪些資源

在開始清除前，先了解需要移除的所有資源清單（依計費優先度排序）：

| 資源 | 模組 | 計費方式 | 月費估計 |
|------|------|----------|----------|
| **NAT Gateway** | network | 按小時計費 | ~$35 (最貴！) |
| **RDS PostgreSQL** | data | 按小時計費 | ~$15 |
| **ElastiCache Redis** | data | 按小時計費 | ~$15 |
| **ALB** | alb | 按小時計費 | ~$18 |
| **ECS Fargate Task (monolith)** | ecspresso | 按 CPU/RAM 計費 | ~$3 |
| **ECS Fargate Task (redpanda)** | messaging | 按 CPU/RAM 計費 | ~$5 |
| **EFS** | messaging | 按 GB 計費 | ~$1 |
| **ECR** | container | 按 GB 儲存計費 | ~$0.1 |
| **CloudWatch Logs** | container | 按儲存量計費 | ~$0.1 |
| **SSM Parameters** | data/root | 免費 (Standard) | $0 |
| **AWS Budgets** | root | 前 2 個免費 | $0 |

> **最高風險**：NAT Gateway 是最貴的隱性費用，必須確認它已被刪除。

---

## 方法一：全自動清除（推薦）

使用 Makefile 提供的一鍵清除指令（需要 `CONFIRM=1` 防止誤觸）：

```bash
cd backend/

# 完整清除：先刪 ECS Service，再刪基礎建設
make destroy-all CONFIRM=1
```

此指令會依序執行：
1. `ecspresso delete --force`（刪除 ECS Service）
2. `terraform destroy -auto-approve`（刪除全部 Terraform 資源）

等待 terraform destroy 完成（約 10～15 分鐘）後，繼續以下**手動驗證步驟**。

---

## 方法二：分步驟清除（更安全）

若一鍵清除失敗或想要更精確控制，可分步驟執行：

### 步驟 1：停止 ECS Service（立即停止計費）

```bash
# 先把 Task 縮到 0（立即停止 Fargate 計費）
aws ecs update-service \
  --cluster exchange-staging \
  --service monolith \
  --desired-count 0 \
  --region ap-northeast-1

aws ecs update-service \
  --cluster exchange-staging \
  --service redpanda \
  --desired-count 0 \
  --region ap-northeast-1
```

### 步驟 2：刪除 ECS Service（透過 ecspresso）

```bash
cd backend/deploy/ecspresso/monolith

# 刪除 ECS Service（不刪 Task Definition）
ecspresso delete --config ecspresso.yml --force
```

### 步驟 3：執行 Terraform Destroy

```bash
cd backend/deploy/terraform/environments/staging

terraform plan -destroy  # 先預覽要刪哪些
terraform destroy        # 輸入 yes 確認
```

---

## 🔍 手動驗證清單（必做）

Terraform Destroy 完成後，**逐一到 AWS Console 確認這些資源已不存在**：

### A. VPC & 網路（最容易漏掉）

```bash
# 確認沒有 exchange-staging VPC
aws ec2 describe-vpcs \
  --filters "Name=tag:Project,Values=exchange" \
  --region ap-northeast-1 \
  --query "Vpcs[*].{VpcId:VpcId,Name:Tags[?Key=='Name']|[0].Value}"
```

**要驗證的項目：**
- [ ] VPC 已刪除
- [ ] **NAT Gateway 已刪除**（最重要！去 Console → VPC → NAT Gateways 確認）
- [ ] Internet Gateway 已刪除
- [ ] 所有 Subnet 已刪除
- [ ] 所有 Security Groups 已刪除（除了 default SG）

NAT Gateway 專項確認：
```bash
aws ec2 describe-nat-gateways \
  --filter "Name=tag:Project,Values=exchange" \
  --region ap-northeast-1 \
  --query "NatGateways[*].{Id:NatGatewayId,State:State}"
```

預期回應：空陣列 `[]`，或 State = `deleted`。

### B. Elastic IP（NAT Gateway 刪除後可能殘留）

```bash
aws ec2 describe-addresses \
  --filters "Name=tag:Project,Values=exchange" \
  --region ap-northeast-1 \
  --query "Addresses[*].{AllocationId:AllocationId,AssociationId:AssociationId}"
```

若有殘留的 Elastic IP（AllocationId 存在但 AssociationId 為空），需手動釋放：
```bash
aws ec2 release-address --allocation-id <AllocationId> --region ap-northeast-1
```

> **費用**：未使用的 Elastic IP 每小時約 $0.005 USD（約一個月 $3.6）。

### C. RDS

```bash
aws rds describe-db-instances \
  --filters "Name=tag:Project,Values=exchange" \
  --region ap-northeast-1 \
  --query "DBInstances[*].{Id:DBInstanceIdentifier,Status:DBInstanceStatus}"
```

預期回應：空陣列 `[]`。

> **注意**：若 `skip_final_snapshot = false`，Terraform destroy 會建立一個手動快照，快照也會計費。確認：
> ```bash
> aws rds describe-db-snapshots \
>   --region ap-northeast-1 \
>   --query "DBSnapshots[?contains(DBSnapshotIdentifier,'exchange')]"
> ```
> 
> 手動刪除不需要的快照：
> ```bash
> aws rds delete-db-snapshot \
>   --db-snapshot-identifier <snapshot-id> \
>   --region ap-northeast-1
> ```

### D. ElastiCache

```bash
aws elasticache describe-replication-groups \
  --region ap-northeast-1 \
  --query "ReplicationGroups[?contains(ReplicationGroupId,'exchange')].{Id:ReplicationGroupId,Status:Status}"
```

預期回應：空陣列 `[]`。

### E. EFS（Redpanda 資料卷）

```bash
aws efs describe-file-systems \
  --region ap-northeast-1 \
  --query "FileSystems[?contains(Tags[?Key=='Project'].Value|[0],'exchange')].{Id:FileSystemId,State:LifeCycleState}"
```

預期回應：空陣列 `[]`。

若 EFS 有殘留，需先刪除 Mount Target 再刪 File System：
```bash
# 列出 Mount Targets
aws efs describe-mount-targets \
  --file-system-id <fs-id> \
  --region ap-northeast-1

# 刪除 Mount Targets（需一個個刪）
aws efs delete-mount-target --mount-target-id <mt-id> --region ap-northeast-1

# 等 Mount Target 刪除後，再刪 File System
aws efs delete-file-system --file-system-id <fs-id> --region ap-northeast-1
```

### F. ALB

```bash
aws elbv2 describe-load-balancers \
  --region ap-northeast-1 \
  --query "LoadBalancers[?contains(LoadBalancerName,'exchange')].{Name:LoadBalancerName,State:State.Code}"
```

預期回應：空陣列 `[]`。

### G. ECS

```bash
# 確認 ECS Cluster 已刪除
aws ecs list-clusters \
  --region ap-northeast-1 \
  --query "clusterArns[?contains(@,'exchange')]"

# 確認沒有殘留 Task（避免 Fargate 計費）
aws ecs list-tasks \
  --cluster exchange-staging \
  --region ap-northeast-1
```

### H. ECR（映像倉庫）

ECR 按儲存量計費（費用較小但仍需確認）：

```bash
aws ecr describe-repositories \
  --region ap-northeast-1 \
  --query "repositories[?contains(repositoryName,'exchange')].repositoryName"
```

若想清除 ECR 內的映像（但保留倉庫）：
```bash
aws ecr list-images \
  --repository-name exchange/staging/monolith \
  --region ap-northeast-1

# 刪除所有映像
aws ecr batch-delete-image \
  --repository-name exchange/staging/monolith \
  --image-ids imageTag=latest \
  --region ap-northeast-1
```

### I. CloudWatch Logs

CloudWatch log groups 按儲存量計費（少量但需確認）：

```bash
aws logs describe-log-groups \
  --log-group-name-prefix "/ecs/exchange" \
  --region ap-northeast-1 \
  --query "logGroups[*].logGroupName"
```

若有殘留（一般 terraform destroy 會一起刪），手動刪除：
```bash
aws logs delete-log-group \
  --log-group-name "/ecs/exchange/staging/monolith" \
  --region ap-northeast-1

aws logs delete-log-group \
  --log-group-name "/ecs/exchange/staging/redpanda" \
  --region ap-northeast-1
```

### J. SSM Parameters

```bash
aws ssm get-parameters-by-path \
  --path /exchange/staging/ \
  --region ap-northeast-1 \
  --query "Parameters[*].Name"
```

SSM Standard Parameters 免費，但若想徹底清理：
```bash
# 刪除所有 /exchange/staging/ 下的參數
aws ssm get-parameters-by-path \
  --path /exchange/staging/ \
  --region ap-northeast-1 \
  --query "Parameters[*].Name" \
  --output text | tr '\t' '\n' | xargs -I{} \
  aws ssm delete-parameter --name {} --region ap-northeast-1
```

### K. Service Discovery（Cloud Map）

```bash
aws servicediscovery list-namespaces \
  --region ap-northeast-1 \
  --query "Namespaces[?Name=='exchange.internal']"
```

若有殘留需手動清除（須先刪 Service 再刪 Namespace）：
```bash
# 先列出並刪除 services
aws servicediscovery list-services --region ap-northeast-1
aws servicediscovery delete-service --id <service-id> --region ap-northeast-1

# 再刪 namespace
aws servicediscovery delete-namespace --id <namespace-id> --region ap-northeast-1
```

---

## 最終費用確認

完成以上步驟後，透過 AWS Cost Explorer 驗證費用歸零：

1. 前往 [AWS Cost Explorer](https://console.aws.amazon.com/cost-management/home)
2. 選擇「Last 7 days」時間範圍
3. 按「Service」分組，確認所有 exchange 相關服務費用趨勢下降至 $0

或使用 CLI 快速確認：
```bash
aws ce get-cost-and-usage \
  --time-period Start=$(date -v-7d +%Y-%m-%d),End=$(date +%Y-%m-%d) \
  --granularity DAILY \
  --metrics "UnblendedCost" \
  --region us-east-1   # Cost Explorer API 固定區域
```

---

## 常見漏掉的資源（排查清單）

若費用未歸零，優先檢查：

| 排查順序 | 資源 | 前往 Console |
|---------|------|-------------|
| 1 | **NAT Gateway** | VPC → NAT Gateways |
| 2 | **Elastic IP** | VPC → Elastic IPs |
| 3 | **RDS 快照** | RDS → Snapshots |
| 4 | **ALB** | EC2 → Load Balancers |
| 5 | **EFS** | EFS → File Systems |
| 6 | **ECS Task（仍在跑）** | ECS → Clusters → exchange-staging → Tasks |

> **黃金原則**：若刪除後 2 小時內費用還沒下降，直接去 [AWS Billing Console](https://console.aws.amazon.com/billing/home) → Cost by Service 找出哪個服務仍在計費。
