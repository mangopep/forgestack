---
name: terraform-plan
description: Explain and validate Terraform safely without applying resources.
---

# terraform-plan
Inspect .tf files and module README. Explain architecture, state, IAM, tagged resources and hourly cost. Use `terraform fmt -check` / `terraform validate` only if Terraform is available and initialization state permits. `terraform plan` may contact AWS; ask before doing it. Do not run apply/destroy.
