.PHONY: help run test race fmt vet build docker-up docker-data docker-analytics docker-down docker-clean smoke
help:
	@echo 'run test race fmt vet build docker-up docker-data docker-analytics docker-down docker-clean smoke'
run:
	go run ./cmd/api
fmt:
	gofmt -w cmd internal
vet:
	go vet ./...
test:
	go test ./... -cover
race:
	go test -race ./...
build:
	go build -o bin/api ./cmd/api
docker-up:
	docker compose up --build -d api
docker-data:
	docker compose --profile data up --build -d
docker-analytics:
	docker compose --profile analytics --profile data up -d
docker-down:
	docker compose --profile data --profile analytics down
docker-clean:
	@echo 'WARNING: this permanently deletes local database volumes.'
	docker compose --profile data --profile analytics down -v
smoke:
	bash scripts/smoke.sh
