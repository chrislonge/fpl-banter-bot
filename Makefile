# Load .env if it exists (ignored by git).
# The `-` prefix tells make to continue even if the file is missing.
-include .env
export

.PHONY: build test test-store test-all lint run db-up db-down db-reset

## Build the bot binary
build:
	go build ./...

## Run all unit tests (no database required)
test:
	go test ./...

## Run store integration tests (requires Postgres)
test-store:
	STORE_TEST_DATABASE_URL=$(STORE_TEST_DATABASE_URL) go test ./internal/store/ -v

## Run all tests including integration
test-all:
	STORE_TEST_DATABASE_URL=$(STORE_TEST_DATABASE_URL) go test ./... -v

## Run the linter
lint:
	golangci-lint run

## Run the bot
run:
	go run cmd/bot/main.go

## Start the database
db-up:
	docker compose up -d db

## Stop the database
db-down:
	docker compose down

## Destroy and recreate the database (needed after schema changes to 001_initial)
db-reset:
	docker compose down -v
	docker compose up -d db
