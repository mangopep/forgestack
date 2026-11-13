# Security Policy

## Supported scope

ForgeStack is a learning/lab repository. The Go and Python reference services and tooling in
this repo are not intended for production use or for handling real user data as-is. Security
reports are still welcome and taken seriously for the code, CI workflows, and Terraform sandbox
in this repository.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for a suspected vulnerability.

Report it privately using GitHub's "Report a vulnerability" feature under this repository's
**Security** tab (Security Advisories), so it can be triaged before any public disclosure.

Please include:
- A description of the issue and its potential impact.
- Steps to reproduce (minimal reproducible example if possible).
- Affected file(s)/commit/version.

## What to expect

- Acknowledgement of the report as soon as practical.
- An assessment of severity and affected scope.
- A fix or mitigation, followed by a public advisory once resolved, crediting the reporter
  unless anonymity is requested.

## Out of scope

- Vulnerabilities requiring physical access to a contributor's machine.
- Issues only present in intentionally insecure demo/teaching code that is explicitly
  documented as such (e.g. the in-memory, unauthenticated API posts endpoint, which is
  documented in `README.md` as not production-ready).
- Findings against third-party dependencies — please report those upstream, and optionally
  reference them here so a version bump can be tracked.

## Secrets and credentials

If you discover committed secrets, credentials, or personal data in this repository's history,
report it the same way (Security Advisories) rather than an issue, so it can be rotated/removed
without wider exposure.
