# Claude Code automation handbook

This is a native Claude Code project setup (not a general prompt folder).

- `../CLAUDE.md`: session-wide project guidance.
- `rules/`: focused Go, security, AWS, teaching and code review standards.
- `skills/*/SKILL.md`: slash-invoked workflows (try `/start-lab`, `/code-review`).
- `agents/*.md`: delegated read-only reviewers and specialized implementers.
- `settings.json`: project tool hooks and permissions; committed to Git.
- `hooks/guard.py`: basic blocking guard; tests in `hooks/test_guard.py`.

## Usage
From project root: `claude`, then `/start-lab` or `/implement-feature`.
Use `/code-review` before opening a PR and `/finish-lab` after completing an experiment.
The GitHub Actions review pipeline is distinct from these local Claude Code skills. AI-written PR comments in GitHub require separate auth/integration and are **not** automatically enabled by this folder. The local `/code-review` skill uses Claude when invoked.

## Safeguards and limitations
`settings.json` does not preapprove cloud mutations. The guard blocks obvious patterns but cannot parse arbitrary shell programs, aliases, scripts, Python code, or indirect API calls. Human authorization, IAM controls, scoped permissions, budgets and code review remain essential.

Claude Code versions evolve; check official docs if commands, hooks or permission options change:
https://code.claude.com/docs/en/settings
https://code.claude.com/docs/en/hooks
https://code.claude.com/docs/en/skills
https://code.claude.com/docs/en/sub-agents
