################################################################################
# Outputs：部署後輸出關鍵資訊
# 執行 terraform output 即可看到以下資訊
################################################################################

output "alb_dns_name" {
  description = "ALB DNS（用於 curl 測試或設定 DNS CNAME）"
  value       = module.alb.alb_dns_name
}

output "ecr_repository_url" {
  description = "ECR URL（docker push 需要）"
  value       = module.container.ecr_repository_url
}

output "ecs_cluster_name" {
  description = "ECS Cluster 名稱（ecspresso deploy 需要）"
  value       = module.container.ecs_cluster_name
}

output "task_execution_role_arn" {
  description = "ECS Task Execution Role ARN（填入 ecs-task-def.json）"
  value       = module.container.task_execution_role_arn
}

output "task_role_arn" {
  description = "ECS Task Role ARN（填入 ecs-task-def.json）"
  value       = module.container.task_role_arn
}

output "target_group_arn" {
  description = "Target Group ARN（填入 ecs-service-def.json）"
  value       = module.alb.target_group_arn
}

output "private_subnet_ids" {
  description = "Private subnet IDs（ecspresso service def 需要）"
  value       = module.network.private_subnet_ids
}

output "sg_ecs_id" {
  description = "ECS 安全群組 ID（ecspresso service def 需要）"
  value       = module.network.sg_ecs_id
}

output "kafka_broker_address" {
  description = "Kafka broker 內部 DNS 位址"
  value       = module.messaging.kafka_broker_address
}

output "log_group_name" {
  description = "CloudWatch log group（查詢 application logs 用）"
  value       = module.container.log_group_name
}
