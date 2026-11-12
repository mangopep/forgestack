# Cloud, IaC, cost and supply-chain invariants
- Prefer local emulators and short-lived sandbox deployments. Every AWS exercise includes estimated hourly/monthly cost, eligible credits caveat, tags, resource inventory and cleanup.
- AWS accounts and cloud state are not disposable merely because they're called sandbox. Never mutate them without user approval.
- Do not auto-apply Terraform; produce `fmt`/`validate`/`plan` only when read-only and configured. Terraform plan can consult cloud APIs and should use a limited identity.
- Pin providers and modules; protect state files and secrets; avoid outputting secrets.
- OIDC + short-lived CI credentials; avoid broad wildcard IAM; never commit secrets or long-lived AWS keys.
- Keep PRs from forks untrusted; review GitHub Actions permissions, third-party action supply chain and cache poisoning risks.
- Distinguish security checks, code review and runtime monitoring. Report coverage gaps honestly.
- Never run chaos or load tests outside explicitly owned/test targets; cap load, duration and spend.
