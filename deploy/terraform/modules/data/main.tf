################################################################################
# Data Module
# 建立 RDS PostgreSQL（主資料庫）與 ElastiCache Redis（快取 / 限流 / Session）
# 兩個資源都部署在 private subnet，只允許 ECS 安全群組存取
################################################################################

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# ------------------------------------------------------------------------------
# RDS Subnet Group（需要至少兩個 AZ）
# ------------------------------------------------------------------------------
resource "aws_db_subnet_group" "main" {
  name       = "${var.project_name}-${var.environment}-rds"
  subnet_ids = var.private_subnet_ids

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-rds-subnet-group"
  })
}

# ------------------------------------------------------------------------------
# RDS PostgreSQL 16
# staging 選用 db.t4g.micro 以控制費用
# ------------------------------------------------------------------------------
resource "aws_db_instance" "main" {
  identifier = "${var.project_name}-${var.environment}-postgres"

  engine         = "postgres"
  engine_version = "16"
  instance_class = var.db_instance_class

  db_name  = var.db_name
  username = var.db_username
  password = var.db_password # ⚠️ 注意：密碼會以明文存在 terraform.tfstate 中（Terraform 的已知限制）
                             # 務必啟用 S3 backend 並開啟 bucket 加密，避免 state 洩露

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [var.sg_rds_id]

  allocated_storage     = 20
  max_allocated_storage = 100 # 自動擴展上限
  storage_type          = "gp3"
  storage_encrypted     = true

  multi_az               = false # staging 單 AZ 節省費用
  publicly_accessible    = false
  skip_final_snapshot    = true  # staging 允許直接刪除

  backup_retention_period = 3
  backup_window           = "03:00-04:00"
  maintenance_window      = "sun:04:00-sun:05:00"

  # 自動小版本升級（安全性修補）
  auto_minor_version_upgrade = true

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-rds"
  })
}

# ------------------------------------------------------------------------------
# ElastiCache Subnet Group
# ------------------------------------------------------------------------------
resource "aws_elasticache_subnet_group" "main" {
  name       = "${var.project_name}-${var.environment}-redis"
  subnet_ids = var.private_subnet_ids

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-redis-subnet-group"
  })
}

# ------------------------------------------------------------------------------
# ElastiCache Redis 7（Cluster Mode 停用，單節點，適合 staging）
# ------------------------------------------------------------------------------
resource "aws_elasticache_replication_group" "main" {
  replication_group_id = "${var.project_name}-${var.environment}-redis"
  description          = "Redis for ${var.project_name} ${var.environment}"

  node_type            = var.redis_node_type
  num_cache_clusters   = 1      # staging 單節點
  port                 = 6379

  engine_version       = "7.1"
  parameter_group_name = "default.redis7"

  subnet_group_name  = aws_elasticache_subnet_group.main.name
  security_group_ids = [var.sg_redis_id]

  at_rest_encryption_enabled = true
  transit_encryption_enabled = true

  automatic_failover_enabled = false # staging 不需要 Multi-AZ
  multi_az_enabled           = false

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-redis"
  })
}

# ------------------------------------------------------------------------------
# SSM Parameter Store：將 endpoint 寫入，供 ECS Task Definition 引用
# ------------------------------------------------------------------------------
resource "aws_ssm_parameter" "database_url" {
  name  = "/${var.project_name}/${var.environment}/DATABASE_URL"
  type  = "SecureString"
  value = "postgres://${var.db_username}:${var.db_password}@${aws_db_instance.main.endpoint}/${var.db_name}?sslmode=require"

  tags = var.tags
}

resource "aws_ssm_parameter" "redis_url" {
  name  = "/${var.project_name}/${var.environment}/REDIS_URL"
  type  = "SecureString"
  value = "rediss://${aws_elasticache_replication_group.main.primary_endpoint_address}:6379"

  tags = var.tags
}
