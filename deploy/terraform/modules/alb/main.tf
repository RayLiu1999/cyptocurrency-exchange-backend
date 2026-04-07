################################################################################
# ALB Module
# Application Load Balancer：對外開放 HTTP，轉發至 gateway 的 8100 port
# Target Group 使用 /health 做健康檢查（而非舊版的 /swagger/index.html）
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
# Application Load Balancer
# ------------------------------------------------------------------------------
resource "aws_lb" "main" {
  name               = "${var.project_name}-${var.environment}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [var.sg_alb_id]
  subnets            = var.public_subnet_ids

  # WebSocket 連線需要長時間保活，ALB 預設 60s 超時會導致 WebSocket 大規模斷線
  # 設置 3600s (1小時) 確保即時行情推播不中斷
  idle_timeout               = 3600
  enable_deletion_protection = false # staging 允許刪除

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-alb"
  })
}

# ------------------------------------------------------------------------------
# Target Group（指向 gateway 任務的 8100 port）
# /health 的健康檢查：2 次成功即視為健康，2 次失敗即移除
# ------------------------------------------------------------------------------
resource "aws_lb_target_group" "app" {
  name        = "${var.project_name}-${var.environment}-tg"
  port        = 8100
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip" # Fargate 必須使用 ip

  health_check {
    path                = "/health"
    protocol            = "HTTP"
    matcher             = "200"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 2
  }

  # 讓進行中的連線在 ECS 滾動部署時優雅結束
  deregistration_delay = 30

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-tg"
  })
}

# ------------------------------------------------------------------------------
# HTTP Listener（port 80 → forward to target group）
# ------------------------------------------------------------------------------
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }

  tags = merge(var.tags, {
    Name = "${var.project_name}-${var.environment}-listener-http"
  })
}
