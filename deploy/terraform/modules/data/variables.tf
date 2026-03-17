variable "project_name" {
  description = "專案名稱"
  type        = string
}

variable "environment" {
  description = "部署環境"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs（來自 network module）"
  type        = list(string)
}

variable "sg_rds_id" {
  description = "RDS 安全群組 ID（來自 network module）"
  type        = string
}

variable "sg_redis_id" {
  description = "Redis 安全群組 ID（來自 network module）"
  type        = string
}

# --- RDS ---
variable "db_instance_class" {
  description = "RDS instance 規格"
  type        = string
  default     = "db.t4g.micro"
}

variable "db_name" {
  description = "預設資料庫名稱"
  type        = string
  default     = "exchange"
}

variable "db_username" {
  description = "RDS 主要帳號"
  type        = string
  default     = "exchange_admin"
}

variable "db_password" {
  description = "RDS 密碼（請透過 terraform.tfvars 或環境變數 TF_VAR_db_password 傳入）"
  type        = string
  sensitive   = true
}

# --- Redis ---
variable "redis_node_type" {
  description = "ElastiCache node 規格"
  type        = string
  default     = "cache.t4g.micro"
}

variable "tags" {
  description = "共用標籤"
  type        = map(string)
  default     = {}
}
