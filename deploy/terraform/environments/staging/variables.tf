variable "project_name" {
  description = "專案名稱，用於所有資源命名前綴"
  type        = string
  default     = "exchange"
}

variable "environment" {
  description = "部署環境"
  type        = string
  default     = "staging"
}

variable "aws_region" {
  description = "AWS Region"
  type        = string
  default     = "ap-northeast-1"
}

variable "vpc_cidr" {
  description = "VPC CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "使用的可用區（staging 使用 2 個 AZ）"
  type        = list(string)
  default     = ["ap-northeast-1a", "ap-northeast-1c"]
}

# --- RDS ---
variable "db_instance_class" {
  description = "RDS instance 規格"
  type        = string
  default     = "db.t4g.micro"
}

variable "db_name" {
  description = "資料庫名稱"
  type        = string
  default     = "exchange"
}

variable "db_username" {
  description = "RDS 帳號"
  type        = string
  default     = "exchange_admin"
}

variable "db_password" {
  description = "RDS 密碼（必須在 terraform.tfvars 或 TF_VAR_db_password 中設定）"
  type        = string
  sensitive   = true
}

# --- Redis ---
variable "redis_node_type" {
  description = "ElastiCache node 規格"
  type        = string
  default     = "cache.t4g.micro"
}

# --- 預算警報 ---
variable "budget_limit" {
  description = "當月費用上限（USD）"
  type        = string
  default     = "3"
}

variable "budget_alert_email" {
  description = "AWS Budgets 超支警報 Email"
  type        = string
}
