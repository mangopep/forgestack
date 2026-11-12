<!-- GENERATED FILE. Do not edit by hand. -->
<!-- Regenerate with: go run ./cmd/docgen generate commands -->

# Command Reference (generated)

Extracted directly from `Makefile`'s `.PHONY` target list, in declared order, with each target's recipe lines — not hand-written. If a command here doesn't match what actually runs, check the raw file.

## `make help`

```bash
echo 'run test race fmt vet lint build docker-up docker-data docker-analytics docker-down docker-clean smoke'
```

## `make run`

```bash
go run ./cmd/api
```

## `make test`

```bash
go test ./... -cover
```

## `make race`

```bash
go test -race ./...
```

## `make fmt`

```bash
gofmt -w cmd internal
```

## `make vet`

```bash
go vet ./...
```

## `make lint`

```bash
golangci-lint run ./...
```

## `make build`

```bash
go build -o bin/api ./cmd/api
```

## `make docker-up`

```bash
docker compose up --build -d api
```

## `make docker-data`

```bash
docker compose --profile data up --build -d
```

## `make docker-analytics`

```bash
docker compose --profile analytics --profile data up -d
```

## `make docker-down`

```bash
docker compose --profile data --profile analytics down
```

## `make docker-clean`

```bash
echo 'WARNING: this permanently deletes local database volumes.'
docker compose --profile data --profile analytics down -v
```

## `make smoke`

```bash
bash scripts/smoke.sh
```

