## Why is this change necessary?

## What changed?

## Tests run
- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] `python3 scripts/review_guard.py --ci`

## Security review
- [ ] No secrets, keys, credentials or personal data committed
- [ ] Input validation / auth / dependency impact considered
- [ ] PR changes don't broaden AWS or GitHub token permissions unexpectedly

## Cost and infrastructure review
- [ ] No infrastructure changes, or estimated cost is documented
- [ ] `terraform plan` reviewed where applicable
- [ ] Teardown / rollback documented

## Failure scenario and observability

## Reviewer notes
