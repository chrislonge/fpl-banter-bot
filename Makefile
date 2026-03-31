# Load .env if it exists (ignored by git).
# The `-` prefix tells make to continue even if the file is missing.
-include .env
export

.PHONY: build test test-store test-telegram test-all lint run backfill docker-backfill notify-test db-up db-down db-reset deploy

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

## Telegram integration test (requires TELEGRAM_BOT_TOKEN + TELEGRAM_CHAT_ID)
test-telegram:
	go test ./pkg/notify/telegram/ -run TestIntegration -v

## Run the linter
lint:
	golangci-lint run

## Run the bot
run:
	go run ./cmd/bot

## Backfill and enrich finished current-season gameweeks (local, requires Postgres running)
backfill:
	go run ./cmd/backfill

## Backfill via Docker Compose (used on Pi or any Docker deployment).
## `--build` keeps the one-shot tool image in sync with the latest code.
docker-backfill:
	docker compose run --rm --build backfill

## Build and start the full stack (db + bot) in detached mode
deploy:
	docker compose up -d --build

## Test the full stats → notify pipeline with real DB data (host-side tool)
## Usage: make notify-test              (latest gameweek)
##        make notify-test GW=12        (specific gameweek)
##        make notify-test DRY_RUN=1    (preview without sending)
##        make notify-test DRY_RUN=1 VERIFY=1 VERIFY_LAST=8
notify-test:
	go run ./cmd/notify-test

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
