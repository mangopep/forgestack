# Cloud cost safety: required procedure

**No `terraform apply`, paid service, or cloud resource is triggered by the CI workflows in this starter.**

1. Confirm the AWS account and region (`aws sts get-caller-identity`, `aws configure list`).
2. Check promotional credit eligibility and expiration; credits may not cover every service.
3. Create AWS Budgets alerts and monitor Cost Explorer. Alerts are not hard caps and may be delayed.
4. Estimate hourly compute, attached storage, public IPv4, NAT Gateway hourly/data processing, load balancer hourly/LCU, cross-AZ transfer, logs, backups, managed Kubernetes control plane, AI token use, and taxes.
5. Tag all experimental resources with project and owner labels (avoid personally identifying labels in a public repo).
6. Inspect `terraform plan` before apply; record budget and intended experiment end.
7. Run `terraform destroy` after experiments, verify state and account inventory across relevant regions. Cloud assets outside Terraform may remain.
8. Check volumes, snapshots, Elastic IP/public IPv4, NAT, load balancers, RDS backups, CloudWatch logs, S3 objects, EKS and queues.
9. Recheck billing after metering catches up; spend reports can lag.

**Important:** local Docker experiments do not use AWS resources, but they use local disk, CPU and RAM. `make docker-clean` deletes local database volumes permanently.

Sample estimates are intentionally not included because live AWS pricing and promotional eligibility change. Use https://calculator.aws/ for the region and instance family of the specific lab.
