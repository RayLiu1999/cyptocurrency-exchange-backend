variable "project_name" {
  description = "專案名稱，用於資源命名前綴"
  type        = string
}

variable "environment" {
  description = "部署環境（staging / production）"
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "要使用的可用區清單"
  type        = list(string)
  default     = ["ap-northeast-1a", "ap-northeast-1c"]
}

variable "tags" {
  description = "套用至所有資源的共用標籤"
  type        = map(string)
  default     = {}
}
