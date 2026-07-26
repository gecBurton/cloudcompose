locals {
  # Environment has extra="forbid", so only emit keys the model declares and
  # drop the optional ones that are unset rather than writing nulls.
  environment = merge(
    {
      target                 = "aws"
      name                   = var.name
      region                 = var.region
      vpc_id                 = aws_vpc.this.id
      public_subnets         = aws_subnet.public[*].id
      private_subnets        = aws_subnet.private[*].id
      ecs_cluster_arn        = aws_ecs_cluster.this.arn
      retain_data_on_destroy = var.retain_data_on_destroy
    },
    var.create_alb ? {
      alb_arn               = aws_lb.this[0].arn
      alb_listener_arn      = aws_lb_listener.this[0].arn
      alb_security_group_id = aws_security_group.alb[0].id
    } : {},
    length(var.tags) > 0 ? { tags = var.tags } : {},
    var.aws_endpoint == null ? {} : { aws_endpoint = var.aws_endpoint },
  )
}

output "environment" {
  description = "Values matching composey's Environment model."
  value       = local.environment
}

output "alb_dns_name" {
  description = "DNS name of the shared ALB, if one was created."
  value       = var.create_alb ? aws_lb.this[0].dns_name : null
}

# Written next to the Terraform state so `composey ... --environment` can consume it directly.
resource "local_file" "environment" {
  filename        = "${path.module}/environment.yml"
  content         = yamlencode(local.environment)
  file_permission = "0644"
}
