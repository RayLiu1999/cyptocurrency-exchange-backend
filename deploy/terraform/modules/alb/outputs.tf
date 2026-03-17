output "alb_dns_name" {
  description = "ALB DNS 名稱（用於測試及 DNS CNAME 設定）"
  value       = aws_lb.main.dns_name
}

output "alb_arn" {
  description = "ALB ARN"
  value       = aws_lb.main.arn
}

output "target_group_arn" {
  description = "Target Group ARN（ecspresso service def 中 load_balancers 需要）"
  value       = aws_lb_target_group.app.arn
}

output "listener_arn" {
  description = "HTTP Listener ARN"
  value       = aws_lb_listener.http.arn
}
