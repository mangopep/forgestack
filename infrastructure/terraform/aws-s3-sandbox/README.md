# AWS S3 sandbox (opt-in, real AWS charges possible)

**Default:** `enable_resources=false`, so `terraform apply` will create no resources. Configure secure non-root AWS credentials first. Never commit state, credentials or `.tfvars` files.

1. `terraform init` — fetch provider (reads network).
2. `terraform fmt -check` — check formatting.
3. `terraform validate` — catch invalid syntax.
4. `terraform plan` — inspect expected changes (default: zero).
5. To opt in, `terraform plan -var='enable_resources=true' -var='bucket_name=replace-with-globally-unique-name'`.
6. After reviewing costs, `terraform apply` with the same two `-var` flags.
7. `terraform destroy` with the same two `-var` flags, then confirm no billable resources remain. Remove stored objects before destroying a nonempty bucket.

This example uses local state **for learning only**. Learn remote encrypted state and locking before team workflows. State may contain secrets even if this example has none.
