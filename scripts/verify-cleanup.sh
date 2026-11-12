#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' 'Local Docker containers:'
docker compose --profile data --profile analytics ps
printf '%s\n' 'Terraform state inspection (if initialized):'
if command -v terraform >/dev/null 2>&1 && [ -d infrastructure/terraform/aws-s3-sandbox/.terraform ]; then
  (cd infrastructure/terraform/aws-s3-sandbox && terraform state list)
else
  echo 'Terraform not initialized here.'
fi
printf '%s\n' 'NOTE: This does NOT prove your AWS account has no other billable resources.'
