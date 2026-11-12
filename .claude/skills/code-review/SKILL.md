---
name: code-review
description: Perform a rigorous read-only review of changed code and infrastructure.
---

# code-review
Read `git status --short` and `git diff` (including staged changes). If no diff, review the user-specified scope. Analyze logic, auth, performance, concurrency, observability, supply chain and cost. Run safe read-only tests if useful. Output only prioritized actionable findings with file/line, severity, reproduction, suggestion; then coverage gaps. Never edit or deploy.
