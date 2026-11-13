#!/usr/bin/env python3
"""Checks a `gh attestation verify --format json` result against the
digest, source commit, and ref a release run actually expects.

Real GitHub attestations can carry MULTIPLE subjects in one statement
(e.g. supply-chain-attest.yml's SBOM attestation covers three files in
a single call) -- blindly trusting `subject[0]` would silently check the
wrong file's digest if the one this caller cares about happens not to be
first. This searches all subjects in all returned entries for the exact
expected digest, and fails closed if none match, if more than one entry
matches but disagrees with itself, or if the matching entry's source
commit or ref is wrong.

Usage: check-attestation-match.py <result-json> <expected-bare-digest> \
         <expected-commit> <expected-ref> <label>
"""
import json
import sys


def fail(label: str, message: str) -> None:
    print(f"FAIL: {label}: {message}", file=sys.stderr)
    sys.exit(1)


def main() -> None:
    if len(sys.argv) != 6:
        print(
            "usage: check-attestation-match.py <result-json> "
            "<expected-bare-digest> <expected-commit> <expected-ref> <label>",
            file=sys.stderr,
        )
        sys.exit(2)

    raw, expected_bare_digest, expected_commit, expected_ref, label = sys.argv[1:6]

    try:
        result = json.loads(raw)
    except json.JSONDecodeError as exc:
        fail(label, f"gh attestation verify output is not valid JSON: {exc}")
        return

    if not isinstance(result, list) or not result:
        fail(label, "gh attestation verify returned no attestations (missing)")
        return

    if not expected_bare_digest.startswith("sha256:"):
        fail(label, f"expected digest is not in sha256:<hex> form: '{expected_bare_digest}'")
        return
    expected_hex = expected_bare_digest[len("sha256:") :]

    matching_entries = []
    for entry in result:
        verification_result = entry.get("verificationResult")
        if not isinstance(verification_result, dict):
            continue
        statement = verification_result.get("statement", {})
        subjects = statement.get("subject", [])
        for subject in subjects:
            if subject.get("digest", {}).get("sha256") == expected_hex:
                matching_entries.append(verification_result)
                break

    if not matching_entries:
        fail(
            label,
            f"no attestation subject matches the expected digest sha256:{expected_hex} "
            "(mismatched digest, or attestation missing for this subject)",
        )
        return

    for verification_result in matching_entries:
        cert = verification_result.get("signature", {}).get("certificate", {})

        actual_commit = cert.get("sourceRepositoryDigest")
        if not actual_commit:
            fail(label, "matching attestation has no sourceRepositoryDigest field (missing)")
            return
        if actual_commit != expected_commit:
            fail(
                label,
                f"attested source commit ({actual_commit}) does not match the "
                f"expected commit ({expected_commit})",
            )
            return

        actual_ref = cert.get("sourceRepositoryRef")
        if not actual_ref:
            fail(label, "matching attestation has no sourceRepositoryRef field (missing)")
            return
        if actual_ref != expected_ref:
            fail(
                label,
                f"attested ref ({actual_ref}) does not match the expected "
                f"release ref ({expected_ref})",
            )
            return

    print(
        f"OK: {label}: attestation subject digest, source commit, and ref all match "
        f"({len(matching_entries)} matching attestation(s) checked)."
    )


if __name__ == "__main__":
    main()
