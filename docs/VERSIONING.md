---
title: Versioning and upgrades
---

# Versioning and upgrades

**Status: implemented (policy + a validation tool), not yet exercised by a real release.**
See `docs/adr/0006-f5-templates-releases.md` for the decisions behind this and
`docs/RELEASES.md` for the release pipeline this versioning policy feeds into.

## What gets a version, and what SemVer means for each

ForgeStack version-tags four distinct things, and "breaking" means something different
for each:

| Thing | Where it's recorded | A MAJOR bump means... | A MINOR/PATCH bump means... |
|---|---|---|---|
| **Templates** (`templates/go-service`, `templates/python-service`) | `template.json`'s `template_version` | A generated project's file layout, required placeholders, or minimum tool version changed in a way that breaks an already-generated project if copied forward by hand. | New optional capability added (MINOR), or a bug fix with no structural change (PATCH). Already-generated projects are never touched automatically either way -- see "Upgrading" below. |
| **The cross-language contract** (`docs/platform/CONTRACTS.md`) | `template.json`'s `contract_version` | An existing field's meaning changes, or a required field is removed (e.g. renaming `X-Request-Id`). | A new optional field/header is added that an older consumer can safely ignore. |
| **Published APIs** (the Go/Python reference services' own endpoints) | Not yet versioned in a URL/header -- both are pre-1.0, single-version services today. A real breaking API change (removing/renaming a field in `/healthz`'s response, for instance) is itself a MAJOR-class change to the contract above. | -- | -- |
| **Releases** (tagged artifacts -- see `docs/RELEASES.md`) | Git tag (`vX.Y.Z`), carried into the SBOM/attestation metadata | Any of the above crossing a MAJOR boundary, or a deliberate incompatible change to the release artifact itself (e.g. the SBOM schema). | A template/contract MINOR or PATCH bump, or a release-pipeline-only fix with no consumer-visible change. |

## Breaking-change policy

1. A MAJOR version bump requires: a written note in that template's/contract's own
   changelog section explaining exactly what breaks and why, and (for templates) a
   worked migration example in `docs/RELEASES.md`'s upgrade section.
2. A MINOR or PATCH bump never requires any action from someone who already generated a
   project from an earlier version -- additive and fix-only changes are, by definition,
   safe to ignore.
3. **Nothing in this repository ever rewrites a project already generated from a
   template.** `scripts/new-service.sh` copies files once, at generation time; there is
   no "sync" or "update" command that reaches back into a generated project. This is
   deliberate: silently rewriting someone's code because the template changed is a worse
   failure mode than requiring a manual, reviewed merge.

## Compatibility checks

`cmd/semvercheck` validates version strings and classifies the relationship between two
versions:

```bash
go run ./cmd/semvercheck validate v1.2.3        # exits non-zero on a malformed version
go run ./cmd/semvercheck compat v1.2.3 v2.0.0    # prints "breaking" (MAJOR changed)
go run ./cmd/semvercheck compat v1.2.3 v1.3.0    # prints "compatible" (MINOR/PATCH only)
```

This is a validation/classification tool, not an enforcement gate that blocks every PR --
it's wired into `release.yml` (see `docs/RELEASES.md`) to reject a release whose tag isn't
valid SemVer, and is available for anyone to run by hand before deciding whether a change
to a template or the contract deserves a MAJOR bump.

## Upgrading a project generated from an older template version

Since generated projects are never auto-rewritten (see policy #3 above):

1. Check what changed: the template's own version history (this file, plus
   `templates/<name>/template.json`'s `template_version`) and whether it's a MAJOR
   (breaking) or MINOR/PATCH (safe) change.
2. If MINOR/PATCH and you want the improvement: generate a fresh throwaway project from
   the current template, diff it against your existing project's corresponding files
   (e.g. `internal/platform/middleware/middleware.go`), and apply the diff by hand or via
   your own merge tooling. There is no automated migration tool for this today -- it is
   a deliberately manual step (see policy #3).
3. If MAJOR: read the migration note this template's changelog entry is required to
   include (policy #1) before deciding whether to adopt it at all.

## Deprecation

A template or contract version is deprecated by a dated note in this file (not a separate
registry) stating what replaces it and by when support ends. Nothing is deprecated as of
this writing -- F5 introduces template versioning `1.0.0` and contract versioning `1.0.0`,
there is no prior version to deprecate.

## Rollback

- **A template**: rolling back means generating from an earlier tagged version of this
  repository (`git checkout <tag> -- templates/`) -- templates are plain files, not a
  separate published package, so this is a normal `git` operation, not a special
  procedure.
- **A release artifact**: see `docs/RELEASES.md`'s rollback section -- redeploying a
  previous release's immutable digest, never rebuilding "the previous version" fresh
  (which could silently pick up a different dependency snapshot).
