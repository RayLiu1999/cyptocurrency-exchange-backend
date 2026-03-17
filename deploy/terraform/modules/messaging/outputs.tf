output "kafka_broker_address" {
  description = "Kafka broker 位址（內部 DNS，monolith ECS task 使用）"
  value       = "redpanda.${var.project_name}.internal:9092"
}

output "kafka_brokers_ssm_name" {
  description = "KAFKA_BROKERS SSM parameter 名稱"
  value       = aws_ssm_parameter.kafka_brokers.name
}

output "service_discovery_namespace_id" {
  description = "Cloud Map namespace ID"
  value       = aws_service_discovery_private_dns_namespace.main.id
}

output "efs_file_system_id" {
  description = "EFS file system ID（除錯用）"
  value       = aws_efs_file_system.redpanda.id
}
