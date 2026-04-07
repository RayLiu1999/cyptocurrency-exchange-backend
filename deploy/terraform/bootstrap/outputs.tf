output "state_bucket_name" {
  description = "Terraform remote state bucket 名稱"
  value       = aws_s3_bucket.terraform_state.bucket
}

output "lock_table_name" {
  description = "Terraform state lock table 名稱"
  value       = aws_dynamodb_table.terraform_locks.name
}

output "backend_config_snippet" {
  description = "可貼入 environments/staging/main.tf 的 backend 設定片段"
  value       = <<-EOT
  backend "s3" {
    bucket         = "${aws_s3_bucket.terraform_state.bucket}"
    key            = "staging/terraform.tfstate"
    region         = "${var.aws_region}"
    encrypt        = true
    dynamodb_table = "${aws_dynamodb_table.terraform_locks.name}"
  }
  EOT
}