output "role_arn" {
  description = "Add this as the repo Actions variable AWS_ACCEPTANCE_ROLE_ARN."
  value       = aws_iam_role.acceptance.arn
}
