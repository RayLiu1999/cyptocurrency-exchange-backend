output "vpc_id" {
  description = "VPC ID"
  value       = aws_vpc.main.id
}

output "public_subnet_ids" {
  description = "Public subnet IDs（用於 ALB）"
  value       = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  description = "Private subnet IDs（用於 ECS、RDS、Redis、Redpanda）"
  value       = aws_subnet.private[*].id
}

output "sg_alb_id" {
  description = "ALB 安全群組 ID"
  value       = aws_security_group.alb.id
}

output "sg_ecs_id" {
  description = "ECS 任務安全群組 ID"
  value       = aws_security_group.ecs.id
}

output "sg_rds_id" {
  description = "RDS 安全群組 ID"
  value       = aws_security_group.rds.id
}

output "sg_redis_id" {
  description = "Redis 安全群組 ID"
  value       = aws_security_group.redis.id
}

output "sg_redpanda_id" {
  description = "Redpanda 安全群組 ID"
  value       = aws_security_group.redpanda.id
}

output "sg_efs_id" {
  description = "EFS 安全群組 ID"
  value       = aws_security_group.efs.id
}
