#!/usr/bin/env python3
"""Detects a discrepancy this repository has actually hit twice in one day:
docs/security/findings-register.json records a finding as "open, tracked"
(the underlying vulnerability is still present and unresolved), but the
GitHub issue it links to has been closed -- with no linked remediation PR
-- leaving the tracker state silently out of sync with technical reality.
Closing a tracking issue does not fix anything; this check exists so that
divergence is caught automatically instead of noticed by chance during an
unrelated audit (which is how both #35 and #40's premature closures were
actually found).

Read-only: only ever calls GitHub's issues-read endpoint, never writes
anything, and works fine with no token at all (just a lower, unauthenticated
rate limit) -- safe to run from an untrusted fork PR's workflow without
granting it `issues: write` or any other privileged scope.

Usage: check-findings-issue-sync.py <findings-register.json> [--token TOKEN]
Exit 0: every "open, tracked" entry's linked issue is actually open (or has
        no issue_url to check).
Exit 1: at least one "open, tracked" entry's linked issue is closed, or a
        GitHub API call failed outright (fails closed, not silently skipped).
"""

import argparse
import json
import re
import sys
import urllib.error
import urllib.request

ISSUE_URL_RE = re.compile(r"github\.com/([^/]+)/([^/]+)/issues/(\d+)$")


def fetch_issue_state(owner: str, repo: str, number: str, token: str | None) -> str:
    url = f"https://api.github.com/repos/{owner}/{repo}/issues/{number}"
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "forgestack-findings-sync-check"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req, timeout=15) as resp:
        data = json.load(resp)
    return data["state"]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("register_path")
    parser.add_argument("--token", default=None, help="GitHub token (optional -- unauthenticated works too, just rate-limited harder)")
    args = parser.parse_args()

    with open(args.register_path, encoding="utf-8") as f:
        register = json.load(f)

    discrepancies = []
    checked = 0
    for entry in register.get("entries", []):
        issue_url = entry.get("issue_url")
        status = (entry.get("status") or "").strip().lower()
        if not issue_url or not status.startswith("open, tracked"):
            continue

        match = ISSUE_URL_RE.search(issue_url)
        if not match:
            print(f"WARN: {entry.get('id')}: issue_url '{issue_url}' doesn't look like a GitHub issue URL -- skipping", file=sys.stderr)
            continue
        owner, repo, number = match.groups()

        checked += 1
        try:
            state = fetch_issue_state(owner, repo, number, args.token)
        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, KeyError) as exc:
            print(f"FAIL: {entry.get('id')}: could not fetch issue state for {issue_url}: {exc}", file=sys.stderr)
            discrepancies.append((entry.get("id"), issue_url, "FETCH_ERROR"))
            continue

        if state == "closed":
            discrepancies.append((entry.get("id"), issue_url, "closed"))

    if discrepancies:
        print(
            "FAIL: findings-register.json records these as 'open, tracked' (the underlying "
            "finding is still unresolved) but their linked GitHub issue is closed:",
            file=sys.stderr,
        )
        for finding_id, issue_url, state in discrepancies:
            print(f"  {finding_id}: {issue_url} -> {state}", file=sys.stderr)
        print(
            "Closing a tracking issue does not fix anything. Either reopen the issue with "
            "evidence (if the finding is genuinely still unresolved), or update "
            "findings-register.json's status if it has actually been remediated.",
            file=sys.stderr,
        )
        return 1

    print(f"OK: {checked} 'open, tracked' finding(s) checked, all have an open linked issue (or no issue to check).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
