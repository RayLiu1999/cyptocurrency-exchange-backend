output "ecr_repository_url" {
  description = "ECR repository URL（用於 docker push）"
  value       = aws_ecr_repository.app.repository_url
}

output "ecs_cluster_name" {
  description = "ECS cluster 名稱"
  value       = aws_ecs_cluster.main.name
}

output "ecs_cluster_arn" {
  description = "ECS cluster ARN"
  value       = aws_ecs_cluster.main.arn
}

output "task_execution_role_arn" {
  description = "ECS Task Execution Role ARN（ecspresso task def 需要）"
  value       = aws_iam_role.ecs_task_execution.arn
}

output "task_role_arn" {
  description = "ECS Task Role ARN（ecspresso task def 需要）"
  value       = aws_iam_role.ecs_task.arn
}

output "log_group_name" {
  description = "CloudWatch log group 名稱"
  value       = aws_cloudwatch_log_group.app.name
}
