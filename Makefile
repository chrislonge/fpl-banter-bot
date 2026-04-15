# Load .env if it exists (ignored by git).
# The `-` prefix tells make to continue even if the file is missing.
-include .env
export

.PHONY: build test test-store test-telegram test-all lint run backfill docker-backfill notify-test db-up db-down db-reset deploy release

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
	@go run ./cmd/bot

## Backfill and enrich finished current-season gameweeks (local, requires Postgres running)
backfill:
	go run ./cmd/backfill

## Backfill via Docker Compose (used on Pi or any Docker deployment).
docker-backfill:
	docker compose run --rm backfill

## Build and start the full stack (db + bot) in detached mode
deploy:
	docker compose up -d

## Cut a release. Bumps docker-compose.yml to the new major.minor, commits,
## tags, and pushes. CI builds and pushes the ARM64 Docker image on tag push.
## Usage: make release VERSION=0.6.0
release: lint test
	@test -n "$(VERSION)" || (echo "Error: VERSION is required. Usage: make release VERSION=x.y.z"; exit 1)
	$(eval MINOR := $(shell echo $(VERSION) | cut -d. -f1,2))
	sed -i '' 's|fpl-banter-bot:[0-9]*\.[0-9]*|fpl-banter-bot:$(MINOR)|g' docker-compose.yml
	git add docker-compose.yml
	git commit -m "Release v$(VERSION)"
	git tag v$(VERSION)
	git push origin main
	git push origin v$(VERSION)

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
