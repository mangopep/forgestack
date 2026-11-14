#!/usr/bin/env python3
"""Fails if any docs/security/findings-register.json entry's review_date
has passed, so a tracked exception can't silently persist past its own
review deadline forever.

This is distinct from scripts/check-npm-audit-baseline.py, which only
checks website/.security/npm-audit-baseline.json's own review_by field --
that file and the register are two separate documents that could drift
out of sync with each other. This checks the register itself, covering
every tracked finding (currently npm and the Python base image), not
just the npm one.

Usage: check-findings-register-expiry.py <findings-register.json>
Exit 0: no entry's review_date has passed (or an entry has none to check).
Exit 1: at least one entry's review_date has passed, or the file itself
        is missing/malformed (fails closed, never silently skipped).
"""

import datetime
import json
import sys


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} <findings-register.json>", file=sys.stderr)
        return 1

    with open(sys.argv[1], encoding="utf-8") as f:
        register = json.load(f)

    today = datetime.date.today()
    expired = []
    checked = 0

    for entry in register.get("entries", []):
        review_date = entry.get("review_date")
        if not review_date:
            continue
        checked += 1
        try:
            due = datetime.date.fromisoformat(review_date)
        except ValueError:
            print(
                f"FAIL: {entry.get('id')}: review_date '{review_date}' is not a valid "
                "ISO date (YYYY-MM-DD)",
                file=sys.stderr,
            )
            expired.append(entry.get("id"))
            continue
        if today > due:
            expired.append(entry.get("id"))
            print(
                f"FAIL: {entry.get('id')}: review_date ({due}) has passed (today: {today})",
                file=sys.stderr,
            )

    if expired:
        print(
            "This exception must be re-reviewed before the finding can keep being "
            "tracked as-is -- confirm it's still unavoidable and update review_date, "
            "or close it out with a real fix.",
            file=sys.stderr,
        )
        return 1

    print(f"OK: {checked} finding(s) with a review_date checked, none expired.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
