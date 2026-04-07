# Terraform Remote State Bootstrap

本目錄負責建立 Terraform remote state 所需的最小資源：

| 資源 | 用途 |
|------|------|
| `S3 bucket` | 儲存 remote `terraform.tfstate` |
| `DynamoDB table` | Terraform state lock |

## 使用方式

```bash
cd deploy/terraform/bootstrap
cp terraform.tfvars.example terraform.tfvars
terraform init
terraform plan
terraform apply
```

完成後，將 `terraform output backend_config_snippet` 的內容貼回 `deploy/terraform/environments/staging/main.tf` 的 `backend "s3"` 區塊，然後執行：

```bash
cd deploy/terraform/environments/staging
terraform init -reconfigure
```

或使用 Makefile：

```bash
make bootstrap-init
make bootstrap-plan
make bootstrap-apply
make infra-init TERRAFORM_INIT_FLAGS=-reconfigure
```

## 注意事項

| 項目 | 說明 |
|------|------|
| **Bucket 名稱唯一性** | S3 bucket 名稱是全域唯一，若衝突請改 `state_bucket_name` |
| **legacy monolith ecspresso** | 目前仍依賴本地 `terraform.tfstate` path，remote state 啟用後需隨 ECS-04 / ECS-05 一起重整 |
| **不要手動刪 state bucket** | 若 staging / production Terraform 已使用該 backend，直接刪除會破壞後續變更流程 |