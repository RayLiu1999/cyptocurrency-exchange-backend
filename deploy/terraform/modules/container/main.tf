################################################################################
# Container Module
# 建立 ECR repository、ECS Cluster、IAM execution role、CloudWatch log group
# ECS Service 與 Task Definition 由 ecspresso 管理（不在 Terraform state 中）
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
# ECR Repository（存放 Go 應用程式 Docker image）
# ------------------------------------------------------------------------------
resource "aws_ecr_repository" "app" {
  name                 = "${var.project_name}/${var.environment}/monolith"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-ecr"
  })
}

# 保留最新 10 個 image，自動清除舊版本以控制儲存費用
resource "aws_ecr_lifecycle_policy" "app" {
  repository = aws_ecr_repository.app.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep last 10 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 10
      }
      action = { type = "expire" }
    }]
  })
}

# ------------------------------------------------------------------------------
# ECS Cluster（Fargate capacity provider）
# ------------------------------------------------------------------------------
resource "aws_ecs_cluster" "main" {
  name = "${var.project_name}-${var.environment}"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-cluster"
  })
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE"
    weight            = 1
    base              = 1
  }
}

# ------------------------------------------------------------------------------
# CloudWatch Log Group（保留 7 天，控制費用）
# ------------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${var.project_name}/${var.environment}/monolith"
  retention_in_days = 7

  tags = var.tags
}

# ------------------------------------------------------------------------------
# IAM：ECS Task Execution Role
# 讓 ECS 從 ECR 拉 image、從 SSM 讀取 secrets、寫入 CloudWatch Logs
# ------------------------------------------------------------------------------
resource "aws_iam_role" "ecs_task_execution" {
  name = "${var.project_name}-${var.environment}-ecs-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "ecs_task_execution_managed" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# 允許從 SSM Parameter Store 讀取 /exchange/<env>/* 的所有參數
# 注意：SecureString 需要 KMS 解密權限，缺少會導致 ECS 任務啟動失敗 (ResourceInitializationError)
resource "aws_iam_role_policy" "ecs_ssm_read" {
  name = "ssm-read"
  role = aws_iam_role.ecs_task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ssm:GetParameter",
        "ssm:GetParameters",
        "ssm:GetParametersByPath",
        "kms:Decrypt"
      ]
      Resource = [
        "arn:aws:ssm:*:*:parameter/${var.project_name}/${var.environment}/*",
        "arn:aws:kms:*:*:alias/aws/ssm"
      ]
    }]
  })
}

# ------------------------------------------------------------------------------
# IAM：ECS Task Role（應用程式執行時的權限）
# ------------------------------------------------------------------------------
resource "aws_iam_role" "ecs_task" {
  name = "${var.project_name}-${var.environment}-ecs-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = var.tags
}

# 應用程式寫入 CloudWatch Logs
resource "aws_iam_role_policy" "ecs_task_logs" {
  name = "cloudwatch-logs"
  role = aws_iam_role.ecs_task.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "logs:CreateLogStream",
        "logs:PutLogEvents"
      ]
      Resource = "${aws_cloudwatch_log_group.app.arn}:*"
    }]
  })
}
