# Engineering Academy: curriculum and practical milestones

Every lab follows: **understand -> build -> test -> break -> observe -> diagnose -> fix -> secure -> review -> document -> destroy temporary cloud resources**.

| Module | Skills | Build / break exercise | Evidence |
|---|---|---|---|
| 00 | Mac M4, shells, Git, Homebrew | inspect environment and install tools | setup transcript |
| 01 | Linux files, permissions, processes, signals, systemd | kill service and recover | incident note |
| 02 | Networking, DNS, TLS, TCP, curl, dig, tcpdump | debug a blocked port | packet trace |
| 03 | Go HTTP, APIs, tests, errors, context | make and load-test API | tests + benchmark |
| 04 | SQL/Postgres, indexes, transactions, locks | slow query and deadlock | EXPLAIN output |
| 05 | Redis, cache, TTL, fallbacks | simulate cache outage | latency comparison |
| 06 | ClickHouse OLAP analytics, partitions, data ingestion | query event workloads | query plan |
| 07 | Docker, Compose, networking, ARM64/AMD64 | crash/restart container | Docker logs |
| 08 | IAM, SSO/Identity Center, CLI, CloudShell, budgets | permission denied lab | least privilege policy |
| 09 | AWS VPC, EC2, S3, EBS, ELB, ASG | stop instance and observe health | AWS diagram |
| 10 | RDS, Aurora, DynamoDB, Lambda, API Gateway | compare service tradeoffs | decision record |
| 11 | Terraform, state, modules, drift, CloudFormation/CDK | plan/apply/destroy | sanitized plan |
| 12 | GitHub Actions, PR rules, SAST/SCA/secret scanning | intentionally failing CI | CI report |
| 13 | Supply-chain, SBOM, signing, provenance, OIDC | block unsafe deploy | attestation |
| 14 | Queues, SQS, Kafka/NATS, retries, DLQ | duplicate message | idempotency proof |
| 15 | Distributed systems, consistency, sharding, rate limits | overload API | load-test report |
| 16 | Kubernetes, Helm, ingress, HPA, RBAC, GitOps | kill pod and rollback | kubectl events |
| 17 | Observability, logs, metrics, traces, SLOs | diagnose high latency | Grafana dashboard |
| 18 | Security, OWASP, API auth, threat modeling, KMS | access-control regression | security test |
| 19 | Chaos engineering, DR, backups, recovery, RTO/RPO | restore failed database | recovery runbook |
| 20 | FinOps, tagging, pricing, NAT/data transfer/logging cost | compare architectures | cost report |
| 21 | AWS Bedrock, RAG, embeddings, model eval | deploy bounded-cost AI feature | evaluation |
| 22 | Platform engineering, multi-account sandbox, policy-as-code | build preview environment | GitOps repo |
| 23 | AWS Solutions Architect Associate SAA-C03 | scenario questions after labs | readiness tracker |
| 24 | Certified Kubernetes Administrator CKA | timed kubectl troubleshooting | readiness tracker |

Also cover production code review, performance profiles, pprof, race detection, zero-downtime releases, security scanning, dependency update automation, AWS quotas, regional resilience, SOC2/CIS concepts, cost quotas, and operational ownership.

## Completion standard
Explain every command and flag; show expected output, risk, and safe undo. Record evidence in `docs/incidents/` and `docs/cost-reports/`.
