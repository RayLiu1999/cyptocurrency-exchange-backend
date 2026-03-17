output "rds_endpoint" {
  description = "RDS PostgreSQL endpoint（host:port）"
  value       = aws_db_instance.main.endpoint
}

output "rds_identifier" {
  description = "RDS instance identifier"
  value       = aws_db_instance.main.identifier
}

output "redis_primary_endpoint" {
  description = "ElastiCache Redis primary endpoint"
  value       = aws_elasticache_replication_group.main.primary_endpoint_address
}

output "database_url_ssm_arn" {
  description = "DATABASE_URL SSM parameter ARN（ecspresso secrets 引用）"
  value       = aws_ssm_parameter.database_url.arn
}

output "redis_url_ssm_arn" {
  description = "REDIS_URL SSM parameter ARN（ecspresso secrets 引用）"
  value       = aws_ssm_parameter.redis_url.arn
}
