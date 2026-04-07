################################################################################
# Messaging Module
# 在 ECS Fargate 上運行單節點 Redpanda（Kafka 相容）
# 使用 EFS 持久化 broker 資料，避免容器重啟後資料遺失
# 透過 ECS Service Discovery（Cloud Map）提供內部 DNS
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
# EFS File System（Redpanda broker 資料持久化）
# ------------------------------------------------------------------------------
resource "aws_efs_file_system" "redpanda" {
  creation_token = "${var.project_name}-${var.environment}-redpanda"
  encrypted      = true

  lifecycle_policy {
    transition_to_ia = "AFTER_7_DAYS"
  }

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-efs-redpanda"
  })
}

# EFS Mount Target（每個 private subnet 各一個）
resource "aws_efs_mount_target" "redpanda" {
  count = length(var.private_subnet_ids)

  file_system_id  = aws_efs_file_system.redpanda.id
  subnet_id       = var.private_subnet_ids[count.index]
  security_groups = [var.sg_efs_id]
}

# EFS Access Point（限定掛載路徑與 POSIX uid/gid）
resource "aws_efs_access_point" "redpanda" {
  file_system_id = aws_efs_file_system.redpanda.id

  posix_user {
    uid = 101 # redpanda image 預設用戶
    gid = 101
  }

  root_directory {
    path = "/redpanda"
    creation_info {
      owner_uid   = 101
      owner_gid   = 101
      permissions = "755"
    }
  }

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-efs-ap-redpanda"
  })
}

# ------------------------------------------------------------------------------
# CloudWatch Log Group（Redpanda container logs）
# ------------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "redpanda" {
  name              = "/ecs/${var.project_name}/${var.environment}/redpanda"
  retention_in_days = 7

  tags = var.tags
}

# ------------------------------------------------------------------------------
# IAM：Redpanda ECS Task & Execution Role
# ------------------------------------------------------------------------------
resource "aws_iam_role" "redpanda_task" {
  name = "${var.project_name}-${var.environment}-redpanda-task"

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

resource "aws_iam_role" "redpanda_execution" {
  name = "${var.project_name}-${var.environment}-redpanda-execution"

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

resource "aws_iam_role_policy_attachment" "redpanda_execution_managed" {
  role       = aws_iam_role.redpanda_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# EFS 掛載授權（IAM 模式）- 與 authorization_config.iam = "ENABLED" 搭配
resource "aws_iam_role_policy" "redpanda_efs_access" {
  name = "efs-access"
  role = aws_iam_role.redpanda_task.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "elasticfilesystem:ClientMount",
        "elasticfilesystem:ClientWrite",
        "elasticfilesystem:ClientRootAccess"
      ]
      Resource = aws_efs_file_system.redpanda.arn
    }]
  })
}

# ------------------------------------------------------------------------------
# Cloud Map Namespace（ECS Service Discovery 用）
# 服務註冊後可透過 redpanda.exchange.internal:9092 連線
# ------------------------------------------------------------------------------
resource "aws_service_discovery_private_dns_namespace" "main" {
  name        = "${var.project_name}.internal"
  vpc         = var.vpc_id
  description = "Private DNS for ${var.project_name} ${var.environment}"

  tags = var.tags
}

resource "aws_service_discovery_service" "redpanda" {
  name = "redpanda"

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.main.id

    dns_records {
      ttl  = 10
      type = "A"
    }

    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
    failure_threshold = 1
  }

  tags = var.tags
}

# ------------------------------------------------------------------------------
# ECS Task Definition：Redpanda（Kafka-compatible broker）
# ------------------------------------------------------------------------------
resource "aws_ecs_task_definition" "redpanda" {
  family                   = "${var.project_name}-${var.environment}-redpanda"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 512
  memory                   = 1024
  execution_role_arn       = aws_iam_role.redpanda_execution.arn
  task_role_arn            = aws_iam_role.redpanda_task.arn

  container_definitions = jsonencode([{
    name      = "redpanda"
    image     = "docker.redpanda.com/redpandadata/redpanda:v23.3.21"
    essential = true

    portMappings = [{
      containerPort = 9092
      protocol      = "tcp"
      name          = "kafka"
    }]

    command = [
      "redpanda",
      "start",
      "--kafka-addr", "internal://0.0.0.0:9092",
      "--advertise-kafka-addr", "internal://redpanda.${var.project_name}.internal:9092",
      "--smp", "1",
      "--memory", "512M",
      "--mode", "dev-container",
      "--default-log-level=info"
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.redpanda.name
        "awslogs-region"        = var.aws_region
        "awslogs-stream-prefix" = "redpanda"
      }
    }

    healthCheck = {
      command     = ["CMD-SHELL", "rpk cluster health || exit 1"]
      interval    = 30
      timeout     = 10
      retries     = 3
      startPeriod = 120 # Redpanda 掛載 EFS 並初始化需要較長時間，60s 可能不夠
    }
  }])

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-redpanda-task"
  })
}

# ------------------------------------------------------------------------------
# ECS Service：Redpanda
# 注意：Redpanda 是 stateful 服務，desiredCount 固定為 1
# ------------------------------------------------------------------------------
resource "aws_ecs_service" "redpanda" {
  name            = "redpanda"
  cluster         = var.ecs_cluster_arn
  task_definition = aws_ecs_task_definition.redpanda.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  # Redpanda 無法零中斷滾動更新（有狀態服務），使用 DAEMON 不適用 Fargate
  # 停舊啟新以保持資料一致性
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100

  network_configuration {
    subnets          = [var.private_subnet_ids[0]] # 固定在第一個 AZ
    security_groups  = [var.sg_redpanda_id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.redpanda.arn
  }

  # 等待 EFS mount target 就緒後再建立 service
  depends_on = [aws_efs_mount_target.redpanda]

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-redpanda-svc"
  })
}

# ------------------------------------------------------------------------------
# SSM Parameter Store：Kafka broker 位址寫入，供應用服務 ECS tasks 引用
# ------------------------------------------------------------------------------
resource "aws_ssm_parameter" "kafka_brokers" {
  name  = "/${var.project_name}/${var.environment}/KAFKA_BROKERS"
  type  = "String"
  value = "redpanda.${var.project_name}.internal:9092"

  tags = var.tags
}
