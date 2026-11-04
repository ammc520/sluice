.PHONY: test test-db build run dev-up dev-down fmt vet web-test web-build compose-config compose-build

# Production Compose reads .env.production. Fall back to the example so
# `make compose-config` still validates the file on a fresh checkout.
ENV_FILE ?= $(if $(wildcard .env.production),.env.production,.env.production.example)
COMPOSE = docker compose --env-file $(ENV_FILE) -f docker-compose.yml

test:
	go test ./...

test-db:
	TEST_DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable go test ./... -count=1

build:
	go build -o bin/sluice ./cmd/sluice

run:
	go run ./cmd/sluice

dev-up:
	docker compose -f compose.dev.yml up -d

dev-down:
	docker compose -f compose.dev.yml down

fmt:
	gofmt -w cmd internal migrations

vet:
	go vet ./...

web-test:
	cd web && npm run test

web-build:
	cd web && npm run build

compose-config:
	$(COMPOSE) config >/dev/null

compose-build:
	$(COMPOSE) build
