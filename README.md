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
# Edit .env with your league ID and database URL.
# Telegram credentials are optional — see Configuration.
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

### Operating modes

The bot supports two operating modes, controlled by whether Telegram credentials are present:

| Mode | Telegram vars | Behavior |
|------|---------------|----------|
| **Data collection only** | Omitted | Polls the FPL API, persists standings and chip data to Postgres. No notifications sent. |
| **Full** | Both set | Polls and persists data, plus sends banter-worthy alerts to the configured chat when the notification pipeline is enabled. |

> **Current status:** The notification pipeline (stats engine → Notifier) is not yet wired. Both modes currently collect and persist data only. Setting Telegram credentials validates them at startup so the bot is ready when notifications are enabled in a future release.

Data-collection-only mode is useful for building up historical data before enabling notifications, or for running the bot purely as a data pipeline. Setting only one of `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` is treated as a misconfiguration and the bot will refuse to start.

### 4. Backfill historical gameweeks (optional)

If the bot was deployed mid-season, it only has data from the gameweek it first ran. The backfill command populates all previous finished gameweeks:

```bash
make backfill
# or: go run cmd/backfill/main.go
```

This is a one-time operation. It detects which gameweeks are missing from the database and fills the gaps. Running it again is safe (idempotent) — it skips gameweeks that already exist. Press `Ctrl+C` to cancel mid-backfill; progress is saved per-gameweek.

> **Important limitation:** The FPL API does not serve historical standings snapshots — `GetAllH2HStandings()` always returns the *current* cumulative league table. This means backfilled standings (ranks, H2H points) reflect today's values, not what the table looked like at each gameweek. Chip usage IS fully historical (sourced from `GetManagerHistory()`).
>
> The bot tags every snapshot with its provenance in the `gameweek_snapshot_meta` table (`source=backfill, standings_fidelity=synthetic`), so the stats engine knows to skip rank-change and streak alerts for backfilled gameweeks while still using the accurate chip data. Snapshots captured during normal live polling are tagged `source=live, standings_fidelity=historical` and are fully trustworthy.

## Configuration

All configuration is via environment variables. See [`.env.example`](.env.example) for the full list.

| Variable | Required | Description |
|----------|----------|-------------|
| `FPL_LEAGUE_ID` | Yes | Your FPL league ID (find it in the URL of your league's page) |
| `FPL_LEAGUE_TYPE` | No | `h2h` or `classic` (default: `h2h`) |
| `TELEGRAM_BOT_TOKEN` | No | Token from @BotFather (omit for data-collection-only mode) |
| `TELEGRAM_CHAT_ID` | No | Target group chat ID (omit for data-collection-only mode) |
| `DATABASE_URL` | Yes | Postgres connection string |
| `STORE_TEST_DATABASE_URL` | No | Test database connection string (for integration tests) |
| `POLL_IDLE_INTERVAL` | No | Seconds between polls when idle (default: `21600` — 6 hours) |
| `POLL_LIVE_INTERVAL` | No | Seconds between polls during a live gameweek (default: `900` — 15 min) |
| `POLL_PROCESSING_INTERVAL` | No | Seconds between polls while results are processing (default: `600` — 10 min) |
| `LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |

## Project structure

```
cmd/bot/             Main bot entrypoint — wires everything together
cmd/backfill/        Backfill CLI — populates historical gameweeks
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
make backfill     # go run cmd/backfill/main.go (one-time historical data)
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
- **Backfill standings are synthetic** — see the [backfill section](#4-backfill-historical-gameweeks-optional) for details on this FPL API limitation.
- **No alerts yet** — the poller collects and persists data, but the stats engine (which generates banter-worthy alerts) and Telegram notifier are not yet implemented.

## License

[MIT](LICENSE)
