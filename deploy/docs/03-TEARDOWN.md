# 03 — 微服務 staging 完整清除指南

本文件說明如何完整移除 staging 的 ECS 微服務與 Terraform 資源，避免 NAT Gateway、RDS、Redis、ALB 與 Fargate 持續計費。

> 若你已經在 AWS Console 手動刪過部分資源，導致 Terraform state 與實際雲端資源脫節，請優先改看 `10-MANUAL-ZERO-COST-DELETION-CHECKLIST.md`。

## 高風險資源

| 資源 | 來源 | 為何要優先確認 |
|------|------|----------------|
| NAT Gateway | `network` module | 單價高、最容易被忽略 |
| RDS PostgreSQL | `data` module | 長時間持續計費 |
| ElastiCache Redis | `data` module | 長時間持續計費 |
| ALB | `alb` module | 即使沒有流量仍持續計費 |
| 四個 ECS Fargate tasks | `deploy/ecspresso/*` | 若服務未刪除會持續產生 compute 成本 |
| Redpanda + EFS | `messaging` module | Broker 與 EFS 會持續保留成本 |

## 方法一：完整自動清除

```bash
cd /Volumes/KINGSTON/Programming/cyptocurrency_exchange/backend
make destroy-all CONFIRM=1
```

此流程會依序執行：

1. `make ecs-delete-core`
2. `make infra-destroy CONFIRM=1`

> `destroy-all` 會先刪除四個核心服務，再刪除 Terraform 管理的整體基礎設施。若某個 service 尚未建立，流程會略過該 service 並繼續。

## 方法二：分步驟清除

若要保守操作，建議以下順序：

### Step 1：先刪除四個核心服務

```bash
make ecs-delete-core
```

若想手動一個個刪：

```bash
make ecs-delete ECS_SERVICE=gateway
make ecs-delete ECS_SERVICE=market-data-service
make ecs-delete ECS_SERVICE=order-service
make ecs-delete ECS_SERVICE=matching-engine
```

### Step 2：刪除基礎設施

```bash
make infra-destroy CONFIRM=1
```

### Step 3：確認沒有殘留高費用資源

請至少確認以下項目：

| 檢查項目 | 指令 |
|------|------|
| VPC / NAT Gateway | `aws ec2 describe-nat-gateways --filter "Name=tag:Project,Values=exchange" --region ap-northeast-1` |
| RDS | `aws rds describe-db-instances --region ap-northeast-1 --query "DBInstances[?contains(DBInstanceIdentifier,'exchange')]"` |
| Redis | `aws elasticache describe-replication-groups --region ap-northeast-1 --query "ReplicationGroups[?contains(ReplicationGroupId,'exchange')]"` |
| ALB | `aws elbv2 describe-load-balancers --region ap-northeast-1 --query "LoadBalancers[?contains(LoadBalancerName,'exchange')]"` |
| ECS Cluster | `aws ecs list-clusters --region ap-northeast-1 --query "clusterArns[?contains(@,'exchange')]"` |
| EFS | `aws efs describe-file-systems --region ap-northeast-1` |
| Log Groups | `aws logs describe-log-groups --log-group-name-prefix "/ecs/exchange" --region ap-northeast-1` |

## 手動驗證清單

- [ ] `gateway`、`order-service`、`matching-engine`、`market-data-service` 皆已從 ECS Service 清單消失。
- [ ] `exchange-staging` cluster 不再有 running tasks。
- [ ] NAT Gateway 已刪除。
- [ ] RDS 與 ElastiCache replication group 已刪除。
- [ ] ALB 與 target group 已刪除。
- [ ] Redpanda 對應的 EFS 與 mount target 已刪除。
- [ ] `/ecs/exchange/staging/*` log groups 已刪除或確認不再需要。

## 常見殘留與處理方式

| 問題 | 原因 | 處理方式 |
|------|------|----------|
| ECR repository 非空導致 destroy 失敗 | repository 中仍有 image tag | 先刪 image，再重新 `make infra-destroy CONFIRM=1` |
| Terraform state lock 未釋放 | 中途 `Ctrl+C` 或 provider error | `terraform force-unlock -force <lock-id>` |
| 某 ECS service 刪不掉 | service 尚有 task 或 network 資源正在清除 | 先確認 desired count 降為 0，再重試 `make ecs-delete ECS_SERVICE=<service>` |
| EFS 留下 mount targets | Redpanda 停止較慢 | 先刪 mount target，再刪 file system |

## ECR 與 CloudWatch 殘留清理

若不想保留映像與 log：

```bash
REPO_NAME=$(cd deploy/terraform/environments/staging && terraform output -raw ecr_repository_url | cut -d'/' -f2-)

aws ecr list-images \
  --repository-name $REPO_NAME \
  --region ap-northeast-1

aws logs describe-log-groups \
  --log-group-name-prefix "/ecs/exchange/staging" \
  --region ap-northeast-1
```

若 staging state 尚未可讀，請直接在 AWS Console 以實際 repository name 清理，不要猜測名稱。

## 最後確認

完成 teardown 後，建議再做一次成本與資源盤點：

```bash
aws budgets describe-budget \
  --account-id $(aws sts get-caller-identity --query Account --output text) \
  --budget-name "exchange-staging-monthly" \
  --region us-east-1
```

若所有高費用資源已刪除，後續只可能剩下極低成本的 ECR / Logs 殘留；請視是否保留證據決定是否清掉。