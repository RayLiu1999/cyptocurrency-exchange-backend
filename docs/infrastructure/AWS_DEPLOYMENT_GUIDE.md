# AWS 部署指南 (AWS Deployment Guide)

本文件基於 [SYSTEM_DESIGN.md](file:///Volumes/KINGSTON/Programming/Go/exchange/docs/SYSTEM_DESIGN.md) 的架構設計，提供完整的 AWS 上線流程、服務設定、以及各環境配置說明。

---

## 目錄

1. [AWS 服務總覽與費用估算](#1-aws-服務總覽與費用估算)
2. [環境配置對照表](#2-環境配置對照表)
3. [上線前置作業](#3-上線前置作業)
4. [網路架構設定 (VPC)](#4-網路架構設定-vpc)
5. [資料層服務建立](#5-資料層服務建立)
6. [EKS 叢集建立與設定](#6-eks-叢集建立與設定)
7. [應用程式部署](#7-應用程式部署)
8. [監控與可觀測性](#8-監控與可觀測性)
9. [CI/CD Pipeline](#9-cicd-pipeline)
10. [安全性配置](#10-安全性配置)
11. [上線檢查清單](#11-上線檢查清單)
12. [常見問題與注意事項](#12-常見問題與注意事項)

---

## 1. AWS 服務總覽與費用估算

### 需要開啟的 AWS 服務

```mermaid
graph LR
    subgraph "網路與安全"
        VPC["VPC"]
        SG["Security Groups"]
        WAF["WAF"]
        CF["CloudFront"]
        ACM["ACM (SSL)"]
        R53["Route 53"]
    end

    subgraph "運算"
        EKS["EKS"]
        EC2["EC2 (Worker Nodes)"]
        ALB["ALB"]
    end

    subgraph "資料儲存"
        RDS["Aurora PostgreSQL"]
        ECR["ElastiCache Redis"]
        MSK["MSK (Kafka)"]
        S3["S3"]
    end

    subgraph "監控與日誌"
        CW["CloudWatch"]
        CWL["CloudWatch Logs"]
    end

    subgraph "身份與存取"
        IAM["IAM"]
        SM["Secrets Manager"]
    end
```

### 服務用途對照表

| AWS 服務              | 用途                    | 計費模式                |
| --------------------- | ----------------------- | ----------------------- |
| **VPC**               | 隔離的虛擬網路環境      | 免費 (NAT Gateway 收費) |
| **EKS**               | Kubernetes 控制平面託管 | $0.10/hr (~$72/月)      |
| **EC2**               | EKS Worker Nodes        | 依實例類型計費          |
| **ALB**               | 應用程式負載平衡        | $0.0225/hr + 流量費     |
| **Aurora PostgreSQL** | 主要關聯式資料庫        | 依實例 + 儲存量計費     |
| **ElastiCache Redis** | 快取與 Pub/Sub          | 依節點類型計費          |
| **MSK (Kafka)**       | 訊息佇列                | 依 Broker 數量計費      |
| **CloudFront**        | CDN 加速                | 依流量計費              |
| **WAF**               | Web 應用防火牆          | 依規則數 + 請求數計費   |
| **Route 53**          | DNS 託管                | $0.50/hosted zone/月    |
| **ACM**               | SSL 憑證                | 免費 (AWS 發行)         |
| **ECR**               | Docker Image Registry   | 依儲存量計費            |
| **Secrets Manager**   | 機敏資料管理            | $0.40/secret/月         |
| **CloudWatch**        | 監控與日誌              | 依指標數與日誌量計費    |

### 月費估算 (最小可行架構)

> [!NOTE]
> 以下為學習用途的最小配置估算，生產環境需要更高規格。

| 項目                      | 規格                    | 預估月費 (USD) |
| ------------------------- | ----------------------- | -------------- |
| EKS Control Plane         | 1 Cluster               | $72            |
| EC2 Worker Nodes          | 2x t3.medium            | ~$60           |
| Aurora PostgreSQL         | db.t3.medium (Multi-AZ) | ~$100          |
| ElastiCache Redis         | cache.t3.micro          | ~$25           |
| MSK Kafka                 | kafka.t3.small x2       | ~$80           |
| NAT Gateway               | 1 Gateway               | ~$45           |
| ALB                       | 1 Load Balancer         | ~$20           |
| 其他 (S3, CloudWatch, 等) | -                       | ~$20           |
| **總計**                  |                         | **~$420/月**   |

---

## 2. 環境配置對照表

### 三環境架構比較

```mermaid
graph TB
    subgraph "Local (開發)"
        L_App["Go Binary (IDE)"]
        L_DB["PostgreSQL (Docker)"]
        L_Redis["Redis (Docker)"]
        L_Kafka["Kafka (Docker)"]
    end

    subgraph "Staging (測試)"
        S_App["EKS (小規格)"]
        S_DB["Aurora (單節點)"]
        S_Redis["ElastiCache (單節點)"]
        S_Kafka["MSK (2 Brokers)"]
    end

    subgraph "Production (生產)"
        P_App["EKS (HA)"]
        P_DB["Aurora (Multi-AZ)"]
        P_Redis["ElastiCache (Cluster)"]
        P_Kafka["MSK (3 Brokers)"]
    end
```

### 詳細配置對照

| 組件              | Local              | Staging               | Production                           |
| ----------------- | ------------------ | --------------------- | ------------------------------------ |
| **Kubernetes**    | -                  | EKS (1 node)          | EKS (3+ nodes, Multi-AZ)             |
| **PostgreSQL**    | Docker (單節點)    | Aurora db.t3.small    | Aurora db.r5.large (Multi-AZ)        |
| **Redis**         | Docker (單節點)    | ElastiCache t3.micro  | ElastiCache r5.large (Cluster)       |
| **Kafka**         | Docker (單 Broker) | MSK t3.small x2       | MSK m5.large x3 (Multi-AZ)           |
| **Load Balancer** | localhost:8080     | ALB (HTTP)            | CloudFront + WAF + ALB (HTTPS)       |
| **Domain**        | localhost          | staging.example.com   | api.example.com                      |
| **SSL**           | -                  | ACM 憑證              | ACM 憑證                             |
| **Secrets 管理**  | .env 檔案          | Secrets Manager       | Secrets Manager                      |
| **日誌**          | stdout             | CloudWatch Logs       | CloudWatch Logs + S3 歸檔            |
| **監控**          | -                  | kube-prometheus-stack | kube-prometheus-stack + AlertManager |

### 環境變數配置範例

```bash
# ============ Local ============
DATABASE_URL=postgres://user:pass@localhost:5432/exchange
REDIS_URL=redis://localhost:6379
KAFKA_BROKERS=localhost:9092

# ============ Staging ============
DATABASE_URL=postgres://user:pass@staging-db.xxxx.rds.amazonaws.com:5432/exchange
REDIS_URL=staging-redis.xxxx.cache.amazonaws.com:6379
KAFKA_BROKERS=b-1.staging-msk.xxxx.kafka.ap-northeast-1.amazonaws.com:9092

# ============ Production ============
DATABASE_URL=postgres://user:pass@prod-db.xxxx.rds.amazonaws.com:5432/exchange
REDIS_URL=prod-redis.xxxx.cache.amazonaws.com:6379
KAFKA_BROKERS=b-1.prod-msk.xxxx.kafka.ap-northeast-1.amazonaws.com:9092,b-2...
```

---

## 3. 上線前置作業

### 3.1 AWS 帳號設定

```mermaid
flowchart LR
    A["建立 AWS 帳號"] --> B["啟用 MFA"]
    B --> C["建立 IAM Admin User"]
    C --> D["設定 Billing Alerts"]
    D --> E["選擇 Region"]
```

**建議步驟：**

1. **建立 AWS 帳號**：使用信用卡註冊，啟用 Free Tier
2. **啟用 Root MFA**：Security Credentials → MFA → 啟用虛擬 MFA
3. **建立 IAM Admin User**：避免直接使用 Root 帳號
4. **設定帳單警報**：CloudWatch → Billing → 設定預算警報
5. **選擇區域**：建議 `ap-northeast-1` (東京) 或 `ap-southeast-1` (新加坡)

### 3.2 本機工具安裝

```bash
# AWS CLI
brew install awscli
aws configure  # 設定 Access Key

# kubectl
brew install kubectl

# eksctl (EKS 專用 CLI)
brew install eksctl

# Helm (Kubernetes Package Manager)
brew install helm

# Terraform (選用，IaC 工具)
brew install terraform
```

### 3.3 網域與 SSL 準備

- 購買網域 (Route 53 或外部)
- 在 ACM (AWS Certificate Manager) 申請 SSL 憑證
- 驗證網域所有權 (DNS 驗證)

---

## 4. 網路架構設定 (VPC)

### VPC 架構圖

```mermaid
graph TD
    subgraph "VPC: 10.0.0.0/16"
        subgraph "Public Subnets"
            PubA["10.0.1.0/24 (AZ-a)"]
            PubB["10.0.2.0/24 (AZ-b)"]
            PubC["10.0.3.0/24 (AZ-c)"]
            ALB["ALB"]
            NAT["NAT Gateway"]
        end

        subgraph "Private Subnets (App)"
            PrivA["10.0.11.0/24 (AZ-a)"]
            PrivB["10.0.12.0/24 (AZ-b)"]
            PrivC["10.0.13.0/24 (AZ-c)"]
            EKS["EKS Nodes"]
        end

        subgraph "Private Subnets (Data)"
            DataA["10.0.21.0/24 (AZ-a)"]
            DataB["10.0.22.0/24 (AZ-b)"]
            DataC["10.0.23.0/24 (AZ-c)"]
            RDS["Aurora"]
            Redis["ElastiCache"]
            MSK["MSK"]
        end
    end

    Internet["Internet Gateway"] --> PubA & PubB & PubC
    ALB --> PrivA & PrivB & PrivC
    EKS --> DataA & DataB & DataC
    PrivA & PrivB & PrivC --> NAT --> Internet
```

### 建立步驟 (使用 eksctl)

```bash
# eksctl 會自動建立 VPC，或使用現有 VPC
eksctl create cluster \
  --name exchange-cluster \
  --region ap-northeast-1 \
  --nodegroup-name standard-workers \
  --node-type t3.medium \
  --nodes 2 \
  --nodes-min 1 \
  --nodes-max 4 \
  --managed
```

### Security Groups 規劃

| Security Group | 允許來源     | 允許連接埠  | 用途          |
| -------------- | ------------ | ----------- | ------------- |
| `sg-alb`       | 0.0.0.0/0    | 443, 80     | ALB 對外      |
| `sg-eks-nodes` | sg-alb       | 30000-32767 | NodePort 服務 |
| `sg-rds`       | sg-eks-nodes | 5432        | PostgreSQL    |
| `sg-redis`     | sg-eks-nodes | 6379        | Redis         |
| `sg-msk`       | sg-eks-nodes | 9092        | Kafka         |

---

## 5. 資料層服務建立

### 5.1 Aurora PostgreSQL

**Console 建立步驟：**

1. RDS → Create database
2. 選擇 "Amazon Aurora" → "PostgreSQL-Compatible"
3. Templates: "Production" 或 "Dev/Test"
4. Settings:
   - DB cluster identifier: `exchange-db`
   - Master username: `admin`
   - Master password: (設定強密碼)
5. Instance configuration: `db.t3.medium` (學習用)
6. Availability: Multi-AZ (生產) 或 Single-AZ (學習)
7. Connectivity: 選擇建立好的 VPC 與 Private Subnet

**重要設定：**

- Parameter Group: 調整 `max_connections`, `shared_buffers`
- 啟用 Performance Insights
- 設定自動備份 (7-35 天)

### 5.2 ElastiCache Redis

**Console 建立步驟：**

1. ElastiCache → Redis OSS → Create
2. Cluster mode: Disabled (學習) / Enabled (生產)
3. Node type: `cache.t3.micro`
4. Number of replicas: 0 (學習) / 2 (生產)
5. Subnet group: 新建或選擇 Private Subnet

### 5.3 MSK (Managed Kafka)

**Console 建立步驟：**

1. MSK → Create cluster
2. Creation method: "Quick create" 或 "Custom create"
3. Cluster name: `exchange-msk`
4. Broker type: `kafka.t3.small`
5. Number of brokers per AZ: 1 (學習) / 2+ (生產)
6. Storage: 100 GiB per broker

**重要設定：**

- 啟用 TLS 加密
- 設定 broker 日誌輸出到 CloudWatch

---

## 6. EKS 叢集建立與設定

### 6.1 建立 EKS 叢集

```bash
# 方法一：使用 eksctl (推薦初學者)
eksctl create cluster \
  --name exchange-cluster \
  --region ap-northeast-1 \
  --version 1.28 \
  --nodegroup-name workers \
  --node-type t3.medium \
  --nodes 2 \
  --nodes-min 1 \
  --nodes-max 5 \
  --managed \
  --with-oidc \
  --ssh-access \
  --ssh-public-key ~/.ssh/id_rsa.pub
```

### 6.2 設定 kubectl

```bash
# 更新 kubeconfig
aws eks update-kubeconfig --name exchange-cluster --region ap-northeast-1

# 驗證連線
kubectl get nodes
```

### 6.3 安裝必要的 Add-ons

```bash
# 1. AWS Load Balancer Controller (處理 ALB Ingress)
helm repo add eks https://aws.github.io/eks-charts
helm install aws-load-balancer-controller eks/aws-load-balancer-controller \
  -n kube-system \
  --set clusterName=exchange-cluster

# 2. External DNS (自動管理 Route 53 記錄)
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install external-dns bitnami/external-dns \
  --set provider=aws \
  --set aws.zoneType=public

# 3. Cluster Autoscaler
helm repo add autoscaler https://kubernetes.github.io/autoscaler
helm install cluster-autoscaler autoscaler/cluster-autoscaler \
  --set autoDiscovery.clusterName=exchange-cluster \
  --set awsRegion=ap-northeast-1
```

### 6.4 建立 Namespace

```bash
kubectl create namespace exchange-app
kubectl create namespace exchange-monitoring
kubectl create namespace exchange-staging
```

---

## 7. 應用程式部署

### 7.1 建立 ECR Repository

```bash
# 為每個服務建立 Repository
aws ecr create-repository --repository-name exchange/api-service
aws ecr create-repository --repository-name exchange/order-service
aws ecr create-repository --repository-name exchange/matching-engine
aws ecr create-repository --repository-name exchange/wallet-service
```

### 7.2 推送 Docker Image

```bash
# 登入 ECR
aws ecr get-login-password --region ap-northeast-1 | \
  docker login --username AWS --password-stdin <account-id>.dkr.ecr.ap-northeast-1.amazonaws.com

# Build & Push
docker build -t exchange/api-service .
docker tag exchange/api-service:latest <account-id>.dkr.ecr.ap-northeast-1.amazonaws.com/exchange/api-service:latest
docker push <account-id>.dkr.ecr.ap-northeast-1.amazonaws.com/exchange/api-service:latest
```

### 7.3 Kubernetes 資源範例

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-service
  namespace: exchange-app
spec:
  replicas: 2
  selector:
    matchLabels:
      app: api-service
  template:
    metadata:
      labels:
        app: api-service
    spec:
      serviceAccountName: api-service-sa
      containers:
        - name: api-service
          image: <account-id>.dkr.ecr.ap-northeast-1.amazonaws.com/exchange/api-service:latest
          ports:
            - containerPort: 8080
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: db-credentials
                  key: url
          resources:
            requests:
              cpu: "100m"
              memory: "128Mi"
            limits:
              cpu: "500m"
              memory: "512Mi"
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
          readinessProbe:
            httpGet:
              path: /ready
              port: 8080
---
# service.yaml
apiVersion: v1
kind: Service
metadata:
  name: api-service
  namespace: exchange-app
spec:
  selector:
    app: api-service
  ports:
    - port: 80
      targetPort: 8080
---
# ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: api-ingress
  namespace: exchange-app
  annotations:
    kubernetes.io/ingress.class: alb
    alb.ingress.kubernetes.io/scheme: internet-facing
    alb.ingress.kubernetes.io/target-type: ip
    alb.ingress.kubernetes.io/certificate-arn: arn:aws:acm:...
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 80
```

### 7.4 Secrets 管理

```bash
# 方法一：直接建立 K8s Secret
kubectl create secret generic db-credentials \
  --from-literal=url='postgres://user:pass@host:5432/db' \
  -n exchange-app

# 方法二：使用 External Secrets Operator (推薦)
# 從 AWS Secrets Manager 同步到 K8s Secret
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets external-secrets/external-secrets
```

---

## 8. 監控與可觀測性

### 8.1 安裝 kube-prometheus-stack

```bash
# 新增 Helm repo
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# 安裝 (包含 Prometheus, Grafana, Alertmanager)
helm install kube-prometheus prometheus-community/kube-prometheus-stack \
  --namespace exchange-monitoring \
  --set grafana.adminPassword=YourSecurePassword \
  --set prometheus.prometheusSpec.retention=15d \
  --set prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.resources.requests.storage=50Gi
```

### 8.2 監控架構

```mermaid
graph LR
    subgraph "Kubernetes Cluster"
        App["應用 Pods"] -->|/metrics| Prometheus
        Node["Node Exporter"] -->|系統指標| Prometheus
        Kube["kube-state-metrics"] -->|K8s 指標| Prometheus
    end

    Prometheus -->|儲存| PV["Persistent Volume"]
    Prometheus -->|查詢| Grafana
    Prometheus -->|告警| AlertManager
    AlertManager -->|通知| Slack["Slack/PagerDuty"]
```

### 8.3 應用程式 Metrics 暴露

```go
// main.go - 使用 prometheus/client_golang
import (
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
    http.Handle("/metrics", promhttp.Handler())
    http.ListenAndServe(":8080", nil)
}
```

### 8.4 建立 ServiceMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: api-service-monitor
  namespace: exchange-monitoring
spec:
  selector:
    matchLabels:
      app: api-service
  namespaceSelector:
    matchNames:
      - exchange-app
  endpoints:
    - port: http
      path: /metrics
      interval: 15s
```

### 8.5 Grafana Dashboard

預建儀表板推薦：

- **Kubernetes Cluster Overview** (ID: 7249)
- **Node Exporter Full** (ID: 1860)
- **Go Metrics** (ID: 6671)

---

## 9. CI/CD Pipeline

### 整體流程

```mermaid
flowchart LR
    Dev["開發者"] -->|Push| GitHub
    GitHub -->|Trigger| Actions["GitHub Actions"]

    subgraph "CI Pipeline"
        Actions --> Test["單元測試"]
        Test --> Lint["程式碼檢查"]
        Lint --> Build["Build Image"]
        Build --> Push["Push to ECR"]
    end

    subgraph "CD Pipeline"
        Push -->|Dev Branch| Staging["部署 Staging"]
        Push -->|Main Branch| Prod["部署 Production"]
    end

    Staging --> Smoke["冒煙測試"]
    Prod --> Monitor["監控驗證"]
```

### GitHub Actions 範例

```yaml
# .github/workflows/deploy.yml
name: Deploy to EKS

on:
  push:
    branches: [main, develop]

env:
  AWS_REGION: ap-northeast-1
  ECR_REPOSITORY: exchange/api-service
  EKS_CLUSTER: exchange-cluster

jobs:
  build-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v4
        with:
          aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
          aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
          aws-region: ${{ env.AWS_REGION }}

      - name: Login to Amazon ECR
        id: login-ecr
        uses: aws-actions/amazon-ecr-login@v2

      - name: Build and push image
        env:
          ECR_REGISTRY: ${{ steps.login-ecr.outputs.registry }}
          IMAGE_TAG: ${{ github.sha }}
        run: |
          docker build -t $ECR_REGISTRY/$ECR_REPOSITORY:$IMAGE_TAG .
          docker push $ECR_REGISTRY/$ECR_REPOSITORY:$IMAGE_TAG

      - name: Update kubeconfig
        run: aws eks update-kubeconfig --name $EKS_CLUSTER --region $AWS_REGION

      - name: Deploy to Kubernetes
        env:
          IMAGE_TAG: ${{ github.sha }}
        run: |
          kubectl set image deployment/api-service \
            api-service=$ECR_REGISTRY/$ECR_REPOSITORY:$IMAGE_TAG \
            -n exchange-app
```

---

## 10. 安全性配置

### 10.1 IAM 角色規劃

```mermaid
graph TD
    subgraph "IAM Roles"
        EKS_Role["EKS Cluster Role"]
        Node_Role["EKS Node Role"]

        subgraph "IRSA (Pod-level)"
            API_SA["API Service Account"]
            Wallet_SA["Wallet Service Account"]
        end
    end

    EKS_Role -->|管理| EKS["EKS Control Plane"]
    Node_Role -->|EC2 權限| Nodes["Worker Nodes"]
    API_SA -->|讀取| Secrets["Secrets Manager"]
    Wallet_SA -->|讀寫| S3["S3 (Backup)"]
```

### 10.2 IRSA 設定 (IAM Roles for Service Accounts)

```bash
# 建立 IAM OIDC Provider
eksctl utils associate-iam-oidc-provider \
  --cluster exchange-cluster \
  --approve

# 建立 Service Account 與 IAM Role 關聯
eksctl create iamserviceaccount \
  --name api-service-sa \
  --namespace exchange-app \
  --cluster exchange-cluster \
  --attach-policy-arn arn:aws:iam::aws:policy/SecretsManagerReadWrite \
  --approve
```

### 10.3 Network Policies

```yaml
# 限制 Pod 網路存取
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: api-service-policy
  namespace: exchange-app
spec:
  podSelector:
    matchLabels:
      app: api-service
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
      ports:
        - port: 8080
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              name: exchange-app
    - to:
        - ipBlock:
            cidr: 10.0.0.0/16 # VPC 內部 (RDS, Redis, MSK)
```

### 10.4 WAF 規則建議

| 規則類型                                 | 用途                 |
| ---------------------------------------- | -------------------- |
| AWS-AWSManagedRulesCommonRuleSet         | 常見攻擊防護         |
| AWS-AWSManagedRulesSQLiRuleSet           | SQL Injection 防護   |
| AWS-AWSManagedRulesKnownBadInputsRuleSet | 已知惡意輸入         |
| Rate-based Rule                          | 限制單一 IP 請求頻率 |

---

## 11. 上線檢查清單

### Phase 1: 基礎設施 ✅

- [ ] AWS 帳號設定完成 (MFA, IAM Admin)
- [ ] VPC 與 Subnet 建立完成
- [ ] Security Groups 設定完成
- [ ] EKS Cluster 建立且可連線
- [ ] RDS Aurora 建立且可連線
- [ ] ElastiCache Redis 建立且可連線
- [ ] MSK Kafka 建立且可連線

### Phase 2: 應用部署 ✅

- [ ] ECR Repository 建立完成
- [ ] Docker Images 已推送
- [ ] Kubernetes Secrets 已建立
- [ ] Deployment/Service/Ingress 已部署
- [ ] 應用程式健康檢查通過
- [ ] ALB 可正常存取應用

### Phase 3: 網路與安全 ✅

- [ ] Route 53 DNS 記錄設定
- [ ] ACM SSL 憑證驗證通過
- [ ] CloudFront 分佈設定
- [ ] WAF 規則已啟用
- [ ] IRSA 權限設定完成
- [ ] Network Policies 已套用

### Phase 4: 監控與 CI/CD ✅

- [ ] kube-prometheus-stack 安裝完成
- [ ] Grafana Dashboard 可存取
- [ ] AlertManager 告警設定
- [ ] GitHub Actions CI/CD 測試通過
- [ ] 日誌可在 CloudWatch 查看

### Phase 5: 正式上線 ✅

- [ ] 壓力測試完成
- [ ] 災難復原演練完成
- [ ] 備份機制驗證
- [ ] On-call 輪值表建立
- [ ] Runbook 文件完成

---

## 12. 常見問題與注意事項

### 💰 成本相關

| 問題                 | 解決方案                                |
| -------------------- | --------------------------------------- |
| NAT Gateway 費用過高 | 考慮使用 NAT Instance 或 VPC Endpoints  |
| EKS 節點閒置浪費     | 啟用 Cluster Autoscaler，設定最小節點數 |
| 資料傳輸費用         | 盡量使用同 AZ 的服務，減少跨 AZ 流量    |
| 日誌儲存成本         | 設定 CloudWatch Logs 保留期限 (7-30 天) |

### 🔧 常見錯誤

| 問題                     | 原因                      | 解決方案                                            |
| ------------------------ | ------------------------- | --------------------------------------------------- |
| Pod 無法連線 RDS         | Security Group 未開放     | 確認 `sg-rds` 允許來自 `sg-eks-nodes`               |
| ALB 無法建立             | IAM 權限不足              | 確認 AWS Load Balancer Controller 有正確的 IAM Role |
| Secrets Manager 存取失敗 | IRSA 未設定               | 使用 `eksctl create iamserviceaccount`              |
| Prometheus 無指標        | ServiceMonitor label 不符 | 確認 selector 與 Service label 一致                 |

### 🔐 安全最佳實踐

> [!CAUTION] > **絕對不要做的事：**
>
> - 將 AWS Access Key 寫入程式碼或 Git
> - 使用 Root 帳號進行日常操作
> - 將 RDS/Redis 放在 Public Subnet
> - 關閉 Security Group 限制 (0.0.0.0/0 對所有 Port)

> [!TIP] > **建議做法：**
>
> - 使用 IRSA 取代 Access Key
> - 啟用 CloudTrail 記錄所有 API 呼叫
> - 定期輪換密碼與憑證
> - 使用 AWS Config 檢查合規性

### 📚 學習資源

- [eksctl 官方文件](https://eksctl.io/)
- [kube-prometheus-stack GitHub](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack)
- [AWS EKS Workshop](https://www.eksworkshop.com/)
- [Kubernetes 學習路徑](https://kubernetes.io/docs/tutorials/)

---

## 附錄：整體上線流程圖

```mermaid
flowchart TB
    subgraph "Phase 1: 準備"
        A1["AWS 帳號設定"] --> A2["安裝 CLI 工具"]
        A2 --> A3["申請網域 & SSL"]
    end

    subgraph "Phase 2: 基礎設施"
        B1["建立 VPC"] --> B2["建立 EKS"]
        B2 --> B3["建立 RDS"]
        B3 --> B4["建立 Redis"]
        B4 --> B5["建立 MSK"]
    end

    subgraph "Phase 3: 應用部署"
        C1["建立 ECR"] --> C2["Push Images"]
        C2 --> C3["部署 K8s 資源"]
        C3 --> C4["設定 Ingress/ALB"]
    end

    subgraph "Phase 4: 安全與監控"
        D1["設定 WAF"] --> D2["安裝 Prometheus"]
        D2 --> D3["設定告警"]
        D3 --> D4["設定 CI/CD"]
    end

    subgraph "Phase 5: 驗證上線"
        E1["壓力測試"] --> E2["安全掃描"]
        E2 --> E3["上線發布"]
    end

    A3 --> B1
    B5 --> C1
    C4 --> D1
    D4 --> E1
```
