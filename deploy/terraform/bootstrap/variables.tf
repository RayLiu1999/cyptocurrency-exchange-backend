variable "project_name" {
  description = "專案名稱，用於 tag 與預設命名"
  type        = string
  default     = "exchange"
}

variable "environment" {
  description = "bootstrap 資源所屬環境標記"
  type        = string
  default     = "shared"
}

variable "aws_region" {
  description = "AWS Region"
  type        = string
  default     = "ap-northeast-1"
}

variable "state_bucket_name" {
  description = "Terraform remote state S3 bucket 名稱"
  type        = string
  default     = "exchange-terraform-state-bucket"
}

variable "lock_table_name" {
  description = "Terraform state lock DynamoDB table 名稱"
  type        = string
  default     = "exchange-terraform-locks"
}