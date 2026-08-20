output "role_arn" {
  description = "Add this as the repo Actions variable AWS_ACCEPTANCE_ROLE_ARN."
  value       = aws_iam_role.acceptance.arn
}

output "state_bucket" {
  description = "Add this as the repo Actions variable AWS_ACCEPTANCE_STATE_BUCKET."
  value       = aws_s3_bucket.state.bucket
}

output "state_lock_table" {
  description = "Add this as the repo Actions variable AWS_ACCEPTANCE_STATE_TABLE."
  value       = aws_dynamodb_table.state_locks.name
}
