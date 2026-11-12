# GitHub Pages setup (one-time, manual — cannot be done from a workflow file)

`.github/workflows/pages-deploy.yml` builds and deploys the documentation site on every push to
`main` that touches `docs/` or `website/`, using the official `actions/deploy-pages` action.
That action can only publish successfully once Pages is turned on for this repository and set
to the right source — a repository **Settings** change, not something any workflow YAML can
configure for itself. Nobody has done this yet; the site will not go live until you do.

## Steps (one-time)

1. Go to **Settings → Pages** on `github.com/mangopep/forgestack`.
2. Under **Build and deployment → Source**, choose **"GitHub Actions"** (not "Deploy from a
   branch" — that's the older mode and is incompatible with `actions/deploy-pages`).
3. Save. There is nothing else to configure here — no branch or folder to pick, since the
   Actions-based deployment source doesn't use one.
4. The next push to `main` touching `docs/` or `website/` (or a manual run via the Actions tab
   → "Deploy Docs to GitHub Pages" → "Run workflow") will build and publish automatically.
   The site will be reachable at `https://mangopep.github.io/forgestack/` — this exact URL is
   already set as `url`/`baseUrl` in `website/docusaurus.config.js`; if the repository is ever
   renamed, that config and this URL both need updating together.

## What happens automatically after that

- **On every PR**: `docs-check.yml`'s `website` job runs `npm ci && npm run build` — build-only,
  never deploys — so a PR that breaks the site fails before merge, not after.
- **On every push to `main`** (after a PR is reviewed and merged): `pages-deploy.yml` builds
  the same way, then publishes via `actions/deploy-pages`. Concurrent pushes don't race each
  other — a new deploy cancels an older one still in flight (`concurrency: group: pages,
  cancel-in-progress: true`).
- **Nothing deploys on a PR itself** — only on `main`, after your own review and merge. This
  matches "publishing remains approval-gated": the approval is the PR merge; nothing in this
  workflow auto-merges or bypasses that.

## Expected result, failure modes, undo

- **Expected**: a successful run of "Deploy Docs to GitHub Pages" in the Actions tab, then the
  site live at the URL above within a minute or two.
- **Likely failure before step 1–3 above are done**: the `deploy` job fails with an error like
  "Not Found" or "Get Pages site failed" — this is GitHub telling you Pages isn't enabled yet,
  not a bug in the workflow. Do steps 1–3, then re-run the workflow.
- **Cost**: GitHub Pages is free for public repositories. No AWS or paid service involved.
- **Undo**: Settings → Pages → Source → "Disable" (or switch back to "Deploy from a branch" and
  leave it unconfigured). The workflow file can stay in the repo harmlessly either way — it
  will simply fail/no-op without a configured Pages source.
