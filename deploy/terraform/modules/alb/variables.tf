variable "project_name" {
  description = "專案名稱"
  type        = string
}

variable "environment" {
  description = "部署環境"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnet IDs（ALB 必須在 public subnet）"
  type        = list(string)
}

variable "sg_alb_id" {
  description = "ALB 安全群組 ID"
  type        = string
}

variable "tags" {
  description = "共用標籤"
  type        = map(string)
  default     = {}
}
