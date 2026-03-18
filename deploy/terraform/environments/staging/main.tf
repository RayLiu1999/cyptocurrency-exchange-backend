################################################################################
# Staging Environment — Root Module
# 組合所有子模組，建立完整的 staging 基礎設施
#
# 使用方式：
#   cd deploy/terraform/environments/staging
#   cp terraform.tfvars.example terraform.tfvars  # 填入機敏值
#   terraform init
#   terraform plan
#   terraform apply
################################################################################

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # 強烈建議：啟用 S3 remote state（避免 state 遺失）
  # 部署前先執行 bootstrap/main.tf 建立 S3 bucket 與 DynamoDB lock table
  # backend "s3" {
  #   bucket         = "exchange-terraform-state"
  #   key            = "staging/terraform.tfstate"
  #   region         = "ap-northeast-1"
  #   encrypt        = true
  #   dynamodb_table = "exchange-terraform-locks"
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = local.common_tags
  }
}

locals {
  common_tags = {
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# ------------------------------------------------------------------------------
# Module：Network（VPC、子網路、NAT、安全群組）
# ------------------------------------------------------------------------------
module "network" {
  source = "../../modules/network"

  project_name       = var.project_name
  environment        = var.environment
  vpc_cidr           = var.vpc_cidr
  availability_zones = var.availability_zones
  tags               = local.common_tags
}

# ------------------------------------------------------------------------------
# Module：Container（ECR、ECS Cluster、IAM、CloudWatch）
# ------------------------------------------------------------------------------
module "container" {
  source = "../../modules/container"

  project_name = var.project_name
  environment  = var.environment
  tags         = local.common_tags
}

# ------------------------------------------------------------------------------
# Module：Data（RDS PostgreSQL + ElastiCache Redis）
# ------------------------------------------------------------------------------
module "data" {
  source = "../../modules/data"

  project_name       = var.project_name
  environment        = var.environment
  private_subnet_ids = module.network.private_subnet_ids
  sg_rds_id          = module.network.sg_rds_id
  sg_redis_id        = module.network.sg_redis_id

  db_instance_class = var.db_instance_class
  db_name           = var.db_name
  db_username       = var.db_username
  db_password       = var.db_password

  redis_node_type = var.redis_node_type
  tags            = local.common_tags
}

# ------------------------------------------------------------------------------
# Module：Messaging（Redpanda on ECS + EFS）
# ------------------------------------------------------------------------------
module "messaging" {
  source = "../../modules/messaging"

  project_name       = var.project_name
  environment        = var.environment
  aws_region         = var.aws_region
  vpc_id             = module.network.vpc_id
  private_subnet_ids = module.network.private_subnet_ids
  sg_redpanda_id     = module.network.sg_redpanda_id
  sg_efs_id          = module.network.sg_efs_id
  ecs_cluster_arn    = module.container.ecs_cluster_arn
  tags               = local.common_tags
}

# ------------------------------------------------------------------------------
# Module：ALB（Application Load Balancer + Target Group）
# ------------------------------------------------------------------------------
module "alb" {
  source = "../../modules/alb"

  project_name      = var.project_name
  environment       = var.environment
  vpc_id            = module.network.vpc_id
  public_subnet_ids = module.network.public_subnet_ids
  sg_alb_id         = module.network.sg_alb_id
  tags              = local.common_tags
}

# ------------------------------------------------------------------------------
# SSM Parameters：非機密環境設定
# 機密（db_password 等）已在 data module 中設定
# ------------------------------------------------------------------------------
resource "aws_ssm_parameter" "go_env" {
  name  = "/${var.project_name}/${var.environment}/GO_ENV"
  type  = "String"
  value = "production"
  tags  = local.common_tags
}

resource "aws_ssm_parameter" "kafka_allow_auto_create" {
  name  = "/${var.project_name}/${var.environment}/KAFKA_ALLOW_AUTO_CREATE"
  type  = "String"
  value = "false"
  tags  = local.common_tags
}

# ------------------------------------------------------------------------------
# AWS Budgets：超出預算時發送 Email 警報（預算安全防護）
# 當月費用預估超過 $3 USD 時立即通知，避免意外帳單
# ------------------------------------------------------------------------------
resource "aws_budgets_budget" "monthly" {
  name         = "${var.project_name}-${var.environment}-monthly"
  budget_type  = "COST"
  limit_amount = var.budget_limit
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80  # 費用超過上限的 80% 時第一次警報
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"  # 預測值超標就警報，不等到真的花超
    subscriber_email_addresses = [var.budget_alert_email]
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100  # 費用實際超過時再次警報
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.budget_alert_email]
  }
}
