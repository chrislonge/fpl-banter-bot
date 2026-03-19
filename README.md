# fpl-banter-bot

> **Status:** Early development — Phase 1 of 4 in progress. The FPL client, data store, and polling state machine are complete. The stats engine and Telegram notifier are not yet implemented. Running the bot starts the poller, which polls the FPL API on an adaptive schedule and persists gameweek data automatically.

A self-hosted bot that tracks your Fantasy Premier League mini-league and posts banter-worthy stats to your group chat after each gameweek. Built in Go, runs on a Raspberry Pi.

## What it does

The bot watches your H2H mini-league via the FPL API and automatically detects interesting events:

- **Rank changes** — "Sarah moves to 1st place for the first time this season!"
- **Win/loss streaks** — "Marcus just hit a 3-game winning streak!"
- **Chip usage** — "James used Triple Captain on Haaland... and scored 27 points."
- **Gameweek summaries** — high scorer, low scorer, biggest upset
- **H2H results** — who beat who this week, with scores

No manual checking required. Alerts are posted to your group chat automatically.

## Architecture

```mermaid
flowchart LR
    subgraph External
        FPL["FPL API\n(fantasy.premierleague.com)"]
        TG["Telegram Bot API"]
    end

    subgraph "fpl-banter-bot"
        direction TB
        MAIN["cmd/bot/main.go\nEntrypoint + graceful shutdown"]
        CONFIG["config\nEnv vars"]
        CLIENT["fpl.Client\nHTTP client"]
        STORE["store.Store\nPostgres persistence"]
        POLLER["poller\nIdle → Live → Processing"]
        STATS["stats.Engine\nDiff + alert detection"]
        NOTIFY["notify.Notifier\nChat platform interface"]
    end

    subgraph Infrastructure
        PG[("PostgreSQL")]
    end

    MAIN --> CONFIG
    MAIN --> CLIENT
    MAIN --> STORE
    MAIN --> POLLER
    POLLER -- "polls" --> CLIENT
    CLIENT -- "HTTP" --> FPL
    POLLER -- "snapshots" --> STORE
    STORE -- "pgx" --> PG
    POLLER -- "on finalize" --> STATS
    STATS -- "reads prev GW" --> STORE
    STATS -- "alerts" --> NOTIFY
    NOTIFY -- "HTTP" --> TG

    style CONFIG fill:#2d6a2d,stroke:#4a4,color:#fff
    style CLIENT fill:#2d6a2d,stroke:#4a4,color:#fff
    style STORE fill:#2d6a2d,stroke:#4a4,color:#fff
    style MAIN fill:#2d6a2d,stroke:#4a4,color:#fff
    style POLLER fill:#2d6a2d,stroke:#4a4,color:#fff
    style STATS fill:#8b6914,stroke:#c90,color:#fff
    style NOTIFY fill:#8b6914,stroke:#c90,color:#fff
```

> **Legend:** Green = completed, Amber = planned

## Tech stack

- **Go** — single binary, ~15MB Docker image
- **PostgreSQL** — standings history, multi-tenant from day one
- **Telegram Bot API** — chat delivery (more platforms planned via the `Notifier` interface)
- **Docker Compose** — local dev and deployment

## Quick start

### Prerequisites

- Go 1.26+
- Docker and Docker Compose (via [Docker Desktop](https://www.docker.com/products/docker-desktop/) or [OrbStack](https://orbstack.dev/))
- (Optional) [`golang-migrate`](https://github.com/golang-migrate/migrate) CLI — only needed for manual migration management. The bot runs migrations automatically on startup.

```bash
# macOS (optional)
brew install golang-migrate
```

> **Note:** If you have a local Postgres installed (e.g., via Homebrew or Postgres.app), stop it before starting the Docker database to avoid port 5432 conflicts:
> ```bash
> brew services stop postgresql@14  # adjust version as needed
> ```

### 1. Clone and configure

```bash
git clone https://github.com/chrislonge/fpl-banter-bot.git
cd fpl-banter-bot
cp .env.example .env
# Edit .env with your Telegram bot token, chat ID, and league ID
```

`.env.example` is the documented template (committed to git). `.env` holds your local values and is gitignored — never commit it.

### 2. Start the database

```bash
make db-up
# or: docker compose up -d db
```

### 3. Run the bot

```bash
make run
# or: go run cmd/bot/main.go
```

Migrations run automatically on startup — no manual migration step needed. The migration SQL files are embedded in the binary via Go's `//go:embed`, so there are no external files to deploy.

The bot starts the polling state machine, which adaptively polls the FPL API based on the gameweek lifecycle (idle between GWs, frequent during live matches, very frequent while waiting for finalization). When a gameweek finalizes, it collects standings and chip usage data and persists them to Postgres.

**Graceful shutdown:** Press `Ctrl+C` or send `SIGTERM` to trigger a clean shutdown. The bot finishes its current poll cycle, closes database connections, and exits.

## Configuration

All configuration is via environment variables. See [`.env.example`](.env.example) for the full list.

| Variable | Required | Description |
|----------|----------|-------------|
| `FPL_LEAGUE_ID` | Yes | Your FPL league ID (find it in the URL of your league's page) |
| `FPL_LEAGUE_TYPE` | No | `h2h` or `classic` (default: `h2h`) |
| `TELEGRAM_BOT_TOKEN` | Yes | Token from [@BotFather](https://t.me/BotFather) |
| `TELEGRAM_CHAT_ID` | Yes | Target group chat ID |
| `DATABASE_URL` | Yes | Postgres connection string |
| `STORE_TEST_DATABASE_URL` | No | Test database connection string (for integration tests) |
| `POLL_IDLE_INTERVAL` | No | Seconds between polls when idle (default: `21600` — 6 hours) |
| `POLL_LIVE_INTERVAL` | No | Seconds between polls during a live gameweek (default: `900` — 15 min) |
| `POLL_PROCESSING_INTERVAL` | No | Seconds between polls while results are processing (default: `600` — 10 min) |
| `LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |

## Project structure

```
cmd/bot/             Entrypoint — wires everything together
internal/config/     Environment variable loading + validation
internal/fpl/        FPL HTTP client + API response types
internal/poller/     Gameweek lifecycle state machine (Idle → Live → Processing)
internal/stats/      Diff engine + alert detection (planned)
internal/store/      Database interface + Postgres implementation + embedded migrations
pkg/notify/          Notifier interface (public API for chat platforms)
pkg/notify/telegram/ Telegram implementation (planned)
```

`internal/` packages are private to this module (compiler-enforced). `pkg/` is the public API — import `pkg/notify` to build your own chat platform adapter.

## Adding a new chat platform

Implement the `Notifier` interface in [`pkg/notify/notify.go`](pkg/notify/notify.go):

```go
type Notifier interface {
    SendAlerts(ctx context.Context, alerts []Alert) error
}
```

The Telegram implementation in `pkg/notify/telegram/` (coming in Phase 1.6) will serve as the reference. Any struct with a matching `SendAlerts` method satisfies the interface — no registration or `implements` keyword needed.

## Development

A `Makefile` wraps common commands so you don't have to remember flags and connection strings. Run `make` with any target below, or use the raw commands directly.

```bash
make build        # go build ./...
make test         # go test ./... (store tests skip without DB)
make test-store   # store integration tests against real Postgres
make test-all     # all tests including store integration
make lint         # golangci-lint run
make run          # go run cmd/bot/main.go
make db-up        # docker compose up -d db
make db-down      # docker compose down
make db-reset     # destroy + recreate DB (needed after schema changes)
```

The Makefile automatically loads your `.env` file, so variables like `STORE_TEST_DATABASE_URL` are available without typing them.

### Live API tests

The `internal/fpl` package includes integration tests that hit the real FPL API. They are skipped by default to keep `go test ./...` fast and CI-safe.

```bash
# Run all live API tests
FPL_LIVE_TEST=1 go test ./internal/fpl/ -run TestLiveAPI -v

# Run a specific live test
FPL_LIVE_TEST=1 go test ./internal/fpl/ -run TestLiveAPI_Bootstrap -v
FPL_LIVE_TEST=1 go test ./internal/fpl/ -run TestLiveAPI_EventStatus -v
FPL_LIVE_TEST=1 go test ./internal/fpl/ -run TestLiveAPI_H2HStandings -v
FPL_LIVE_TEST=1 go test ./internal/fpl/ -run TestLiveAPI_ManagerHistory -v
```

These tests require network access and will fail if the FPL API is unavailable. Use them to validate that your struct definitions still match the live API responses.

### Store integration tests

The `internal/store` package includes integration tests against a real Postgres database. They are gated behind the `STORE_TEST_DATABASE_URL` env var and skip gracefully without it.

```bash
# Using make (loads .env automatically)
make test-store

# Or manually
STORE_TEST_DATABASE_URL="postgres://fplbot:password@localhost:5432/fplbanterbot_test?sslmode=disable" \
  go test ./internal/store/ -v
```

The test database (`fplbanterbot_test`) is created automatically by [`init.sql`](init.sql) on first Postgres startup — this file runs once when Docker initialises a fresh volume. If your Postgres volume already exists, run `make db-reset` once to recreate it.

### Database management

Migrations run automatically on startup via embedded SQL files — no CLI tool needed for normal use. The `golang-migrate` CLI is optional, useful for manual inspection or rollbacks.

```bash
# Start the dev database
make db-up

# Check container status
docker compose ps

# View database logs
docker compose logs db

# Connect to the database directly
docker compose exec db psql -U fplbot -d fplbanterbot

# Stop the database (data is preserved)
make db-down

# Stop the database and delete all data (fresh start)
make db-reset

# Manual migration management (optional, requires golang-migrate CLI)
migrate -path internal/store/migrations -database "$DATABASE_URL" up
migrate -path internal/store/migrations -database "$DATABASE_URL" down 1
```

## Known limitations

- **H2H leagues only** — classic league support is planned but not yet implemented. The bot fails fast on startup if `FPL_LEAGUE_TYPE` is not `h2h`.
- **No historical backfill** — if the bot is offline for multiple gameweeks, it only captures data for the current gameweek when it comes back. Missed intermediate gameweeks are lost. A backfill command is planned (milestone 4b).
- **No alerts yet** — the poller collects and persists data, but the stats engine (which generates banter-worthy alerts) and Telegram notifier are not yet implemented.

## License

[MIT](LICENSE)
