terraform {
  required_version = ">= 1.7.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project     = "forgestack"
      Environment = "sandbox"
      ManagedBy   = "terraform"
    }
  }
}

variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "enable_resources" {
  type    = bool
  default = false
}

variable "bucket_name" {
  type    = string
  default = ""
  validation {
    condition     = var.bucket_name == "" || length(var.bucket_name) >= 3
    error_message = "bucket_name must have at least 3 characters"
  }
}

resource "aws_s3_bucket" "lab" {
  count  = var.enable_resources ? 1 : 0
  bucket = var.bucket_name
  lifecycle {
    precondition {
      condition     = var.bucket_name != ""
      error_message = "Choose a globally unique bucket_name before enabling resources."
    }
  }
}

resource "aws_s3_bucket_public_access_block" "lab" {
  count                   = var.enable_resources ? 1 : 0
  bucket                  = aws_s3_bucket.lab[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

output "bucket_name" {
  value = var.enable_resources ? aws_s3_bucket.lab[0].id : "nothing created: enable_resources=false"
}
