# Emergency hotfix procedure

This is the **only** sanctioned way to get a change into `main` without going through
`develop` → `staging` first. It exists for genuinely urgent production fixes — not for
"I don't want to wait for the normal flow." Using it for anything else erodes the one exception
the whole policy depends on being rare and visible.

**Status**: documented and enforced in code (`internal/branchpolicy`), not yet live on GitHub —
see `docs/adr/0002-gitflow-branching-strategy.md` for what's applied versus still pending
approval.

## When this applies

Use a hotfix only when production (`main`) is broken or actively harmful *right now*, and
waiting for a normal `develop → staging → main` promotion would make things meaningfully worse
— for example, a crash loop, a security exposure, or data corruption in progress. If the bug is
real but not urgent, open it as a normal `fix/*` branch into `develop` instead.

## The procedure

1. **Branch from `main`**, not from `develop` or `staging` — you're patching what's actually
   running:
   ```bash
   git checkout main && git pull --ff-only
   git checkout -b hotfix/<short-description>
   ```
   *In simple English*: `checkout -b` creates a new branch starting from wherever you currently
   are (here, the latest `main`) and switches you onto it. `pull --ff-only` updates your local
   `main` from GitHub, refusing to do anything confusing (like a merge) if your local copy has
   diverged — it just fails loudly instead, which is what you want before cutting a hotfix.

2. **Make the smallest fix that resolves the urgent problem.** Resist bundling in unrelated
   cleanup — a hotfix under review pressure is exactly the wrong place for scope creep.

3. **Push and open a PR targeting `main`**:
   ```bash
   git push -u origin hotfix/<short-description>
   ```
   *In simple English*: `-u` ("set upstream") tells your local branch to remember this remote
   branch, so future `git push`/`git pull` on this branch don't need you to spell out
   `origin hotfix/<name>` again.

4. **The same CI gates run as any other PR into `main`** — this is "equivalent verification,"
   not a weaker path: `go-quality`, `security-baseline`, `analyze` (CodeQL), `gitleaks`,
   `trivy-image`, `validate` (Terraform), `docs`, and `check` (branch-policy itself, which
   explicitly permits `hotfix/*` → `main` — see `internal/branchpolicy/policy_test.go`). A
   hotfix skips the *branch ladder*, never the *checks*.

5. **Merge into `main`** once checks pass. This resolves the emergency.

6. **Immediately open a second PR, same branch, targeting `develop`** — the backport:
   ```bash
   # from the same hotfix/<short-description> branch, no new branch needed
   ```
   Open the PR in GitHub's UI (or `gh pr create --base develop --head
   hotfix/<short-description>` once authenticated). This is **not automatic** — if you skip it,
   the next time `develop` is promoted through `staging` to `main`, it will silently overwrite
   your hotfix with the pre-fix code, since `develop` never saw the change. The branch-policy
   check explicitly allows this backport PR (`hotfix/*` → `develop` is in the permitted table).

7. **If `staging` already has a commit between the hotfix and the next promotion**, also open a
   backport PR into `staging` directly, for the same reason — don't wait for `develop`'s next
   promotion cycle if that would leave `staging` out of sync with what's actually in production
   for any meaningful length of time.

## What this procedure does not do (known limitation, not hidden)

`branchpolicy` cannot verify that the backport PR in step 6 actually happened — checking that
would mean inspecting git history across time and branches, which is out of scope for a
per-PR check. This is a process responsibility: whoever merges the hotfix is responsible for
opening the backport immediately after, and should say so out loud (PR description, team
channel, wherever relevant) so it isn't forgotten under the same time pressure that produced
the hotfix.

## Why this isn't just "merge to main whenever you want"

If `hotfix/*` → `main` had no distinguishing branch-name convention, there would be no way to
audit, after the fact, which production changes skipped the normal promotion ladder and why.
Requiring the `hotfix/` prefix makes every emergency bypass visible in `git log --all --
grep` and in the PR list — it is a deliberately loud escape hatch, not a quiet one.
