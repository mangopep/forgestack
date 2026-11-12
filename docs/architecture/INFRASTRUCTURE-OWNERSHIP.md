---
title: Infrastructure ownership and environment isolation
---

# Infrastructure ownership and environment isolation

**Status: mostly planned.** Only the sandbox Terraform example (greyed below as "opt-in,
not deployed") exists today. No AWS account structure, no multiple environments, and no
Terraform remote-state backend have been built — this diagram shows the intended shape (per
`docs/COST-SAFETY.md` and the F6/F7 roadmap modules) so later work has a target, not a claim
that it's running.

```mermaid
flowchart TB
    subgraph today["Implemented today"]
        sandbox["infrastructure/terraform/aws-s3-sandbox<br/>enable_resources=false by default<br/>no remote state, local only"]
    end

    subgraph planned["Planned (F6/F7), not built"]
        direction LR
        dev_env["dev environment root"]
        staging_env["staging environment root"]
        prod_env["production environment root"]
        state["Remote state backend<br/>(S3 + native locking)"]
        oidc["GitHub OIDC federation<br/>(no long-lived AWS keys)"]
        iam["Least-privilege IAM roles<br/>per environment"]

        dev_env --> state
        staging_env --> state
        prod_env --> state
        oidc --> iam
        iam --> dev_env
        iam --> staging_env
        iam --> prod_env
    end

    sandbox -.->|will become one of these, once approved| dev_env

    classDef done fill:#2f855a,color:#fff,stroke:#22543d
    classDef todo fill:#718096,color:#fff,stroke:#4a5568,stroke-dasharray: 4 3
    class sandbox done
    class dev_env,staging_env,prod_env,state,oidc,iam todo
```

**Why isolate environments by Terraform root, not just by variable**: a shared Terraform root
with environment-conditional resources means one `apply` can affect every environment's state
at once. Separate roots per environment (even reusing the same modules) bound the blast radius
of any single `apply` to the environment it targets. None of this exists yet — see
`docs/COST-SAFETY.md` for the cost-safety rules that will govern it once it does, and the F1
branch-promotion flow (`main` = production-approved) this will eventually gate deployments
behind.
