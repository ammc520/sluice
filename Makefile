.PHONY: test test-db build run dev-up dev-down fmt vet

test:
	go test ./...

test-db:
	TEST_DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable go test ./... -count=1

build:
	go build -o bin/gateway ./cmd/gateway

run:
	go run ./cmd/gateway

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
	docker compose -f docker-compose.yml config >/dev/null

compose-build:
	docker compose -f docker-compose.yml build
