#!/usr/bin/env python3
"""Compares a real `npm audit --json` run against a tracked baseline.

F4 requirement: "never mark a finding resolved merely because a scanner
suppresses it," and "fast, reliable PR security gates." A blanket
`npm audit --audit-level=high` would permanently fail every PR on
website/'s 26 pre-existing, tracked, dev-tooling-only findings (issue #35)
with no safe fix available yet (npm's own suggested fix is a Docusaurus
downgrade -- investigated and rejected in #35). Comparing against a
reviewed, dated baseline instead means: new findings beyond the baseline
still fail the build; a stale (expired) baseline also fails the build,
forcing a human to re-review rather than let the exception silently persist
forever.

Usage: check-npm-audit-baseline.py <audit.json> <baseline.json>
Exit 0: findings are within the reviewed, non-expired baseline.
Exit 1: new findings exist, baseline is missing/malformed, or expired.
"""

import datetime
import json
import sys


def main() -> int:
    if len(sys.argv) != 3:
        print(f"usage: {sys.argv[0]} <audit.json> <baseline.json>", file=sys.stderr)
        return 1

    audit_path, baseline_path = sys.argv[1], sys.argv[2]

    with open(audit_path, encoding="utf-8") as f:
        audit = json.load(f)
    with open(baseline_path, encoding="utf-8") as f:
        baseline = json.load(f)

    review_by = datetime.date.fromisoformat(baseline["review_by"])
    today = datetime.date.today()
    if today > review_by:
        print(
            f"FAIL: npm-audit-baseline.json review_by ({review_by}) has passed "
            f"(today: {today}). The exception must be re-reviewed -- update "
            f"review_by after confirming the findings are still unavoidable, "
            f"or the fix referenced in issue #35 has landed.",
            file=sys.stderr,
        )
        return 1

    actual = audit.get("metadata", {}).get("vulnerabilities", {})
    baseline_counts = baseline["baseline_counts"]

    new_beyond_baseline = {}
    for severity, baseline_count in baseline_counts.items():
        if severity in ("total",):
            continue
        actual_count = actual.get(severity, 0)
        if actual_count > baseline_count:
            new_beyond_baseline[severity] = (baseline_count, actual_count)

    if new_beyond_baseline:
        print("FAIL: npm audit found NEW vulnerabilities beyond the tracked baseline:", file=sys.stderr)
        for severity, (expected, got) in new_beyond_baseline.items():
            print(f"  {severity}: baseline={expected}, actual={got}", file=sys.stderr)
        print(
            f"See {baseline['issue']} for the tracked baseline, or update it "
            "(with justification) if these are the same known findings re-counted.",
            file=sys.stderr,
        )
        return 1

    print(
        f"OK: {actual.get('total', 0)} findings, within the reviewed baseline "
        f"({baseline_counts['total']}, see {baseline['issue']}). "
        f"Baseline review due by {review_by}."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
