variable "name" {
  type        = string
  description = "Environment name (e.g., production, staging)."
}

variable "region" {
  type        = string
  description = "The AWS region."
  default     = "eu-west-2"
}

variable "vpc_cidr" {
  type        = string
  description = "CIDR block for the VPC."
  default     = "10.0.0.0/16"
}

variable "az_count" {
  type        = number
  description = "Number of availability zones to spread subnets across."
  default     = 2
}

variable "create_alb" {
  type        = bool
  description = "Create a shared ALB and listener. Set false for a compute-only environment."
  default     = true
}

variable "certificate_arn" {
  type        = string
  description = "ACM certificate ARN. When set the shared listener is HTTPS:443, otherwise HTTP:80."
  default     = null
}

variable "aws_endpoint" {
  type        = string
  description = "Custom endpoint for AWS services (e.g., LocalStack). Null for real AWS."
  default     = null
}

variable "tags" {
  type        = map(string)
  description = "Default tags applied to all resources."
  default     = {}
}

variable "retain_data_on_destroy" {
  type        = bool
  description = "Whether tearing down an application preserves its data. False for throwaway environments, which is what the acceptance smoke test needs: teardown must not stop at a non-empty bucket or a final snapshot."
  default     = true
}
