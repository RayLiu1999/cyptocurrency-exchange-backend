variable "project_name" {
  description = "專案名稱，用於資源命名前綴"
  type        = string
}

variable "environment" {
  description = "部署環境（staging / production）"
  type        = string
}

variable "tags" {
  description = "套用至所有資源的共用標籤"
  type        = map(string)
  default     = {}
}
