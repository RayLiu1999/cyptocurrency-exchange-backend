variable "project_name" {
  description = "專案名稱"
  type        = string
}

variable "environment" {
  description = "部署環境"
  type        = string
}

variable "aws_region" {
  description = "AWS Region（CloudWatch logs 需要）"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID（Cloud Map namespace 需要）"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs"
  type        = list(string)
}

variable "sg_redpanda_id" {
  description = "Redpanda 安全群組 ID"
  type        = string
}

variable "sg_efs_id" {
  description = "EFS 安全群組 ID"
  type        = string
}

variable "ecs_cluster_arn" {
  description = "ECS Cluster ARN（來自 container module）"
  type        = string
}

variable "tags" {
  description = "共用標籤"
  type        = map(string)
  default     = {}
}
