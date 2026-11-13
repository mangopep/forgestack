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

## Optional module: Audit & Trust (reusable, not yet operational)

Goal: any product built on ForgeStack should be able to prepare for an enterprise
security review or independent audit using trustworthy, continuously collected
evidence — not a hand-assembled spreadsheet the week before an audit.

This is explicitly optional and phased, not a new always-on service:

- **F4 (implemented now)**: evidence foundations only. `cmd/secreport` already
  produces versioned (`schema_version`), linked (repository/commit/branch; product/
  environment/owner fields added in this phase) evidence from real scanner output,
  with explicit `PASS`/`FAIL`/`BLOCKED` (missing evidence)/`NOT_VERIFIED`/`EXPIRED`
  states — never a fabricated or assumed result. Control mappings exist for SOC 2,
  ISO 27001, ISO 42001, and NIST SSDF as a documentation table (see
  `docs/architecture/SUPPLY-CHAIN-SECURITY.md`), explicitly marked as evidence, not
  certification. Automated collection today covers CI scan results, SBOMs, and
  signed-artifact evidence only.
- **Future, unscheduled against a specific F-number (not started)** — originally
  sketched as "F5-F10" before F5 was assigned to templates/versioning/releases instead
  (see `docs/adr/0006-f5-templates-releases.md`); renumber when this work is actually
  scheduled rather than guess a number now: expand automated collection to infrastructure
  state, access reviews, incident records, backup/recovery verification, and
  security test results — each one a separate, reviewable change, not a single
  rewrite. Add reviewer comments, remediation tasks, and evidence-request tracking
  on top of the existing exception/finding register. Add real tamper-evidence
  (e.g. a hash chain or external timestamping service) if/when retention
  requirements actually justify it — today's evidence retention is explicitly
  **not** immutable (GitHub Actions artifact storage, 90-day retention, ordinary
  repo-write access controls — see `SUPPLY-CHAIN-SECURITY.md`'s retention section).
- **Later, when justified (not started)**: a reusable, product-agnostic "Trust
  Center" — customer-facing evidence sharing with approval workflow, confidentiality
  controls, expiry, access logs, and document versioning; tracking of actual
  independent certifications (issuing authority, verified scope, issuance/expiry
  dates) — **ForgeStack never self-issues a certification**, it only records ones a
  real external auditor issued. This is deliberately not built until a real product
  on ForgeStack needs it — building it speculatively would be exactly the
  unjustified complexity this repository's own engineering principle warns against.

Manual, organizational controls (e.g. "did a human actually review access this
quarter") are tracked separately from automatically-verified technical controls
(e.g. "did Trivy actually run") from the start — conflating the two would let an
unverified manual claim borrow the credibility of a real automated check.

## Completion standard
Explain every command and flag; show expected output, risk, and safe undo. Record evidence in `docs/incidents/` and `docs/cost-reports/`.
