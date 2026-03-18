# fpl-banter-bot

A self-hosted bot that tracks your Fantasy Premier League mini-league and posts banter-worthy stats to your group chat after each gameweek. Built in Go, runs on a Raspberry Pi.

## What it does

The bot watches your H2H mini-league via the FPL API and automatically detects interesting events:

- **Rank changes** — "Sarah moves to 1st place for the first time this season!"
- **Win/loss streaks** — "Marcus just hit a 3-game winning streak!"
- **Chip usage** — "James used Triple Captain on Haaland... and scored 27 points."
- **Gameweek summaries** — high scorer, low scorer, biggest upset
- **H2H results** — who beat who this week, with scores

No manual checking required. Alerts are posted to your group chat automatically.

## Tech stack

- **Go** — single binary, ~15MB Docker image
- **PostgreSQL** — standings history, multi-tenant from day one
- **Telegram Bot API** — chat delivery (more platforms planned via the `Notifier` interface)
- **Docker Compose** — local dev and deployment

## Quick start

### Prerequisites

- Go 1.21+
- Docker and Docker Compose (via [Docker Desktop](https://www.docker.com/products/docker-desktop/) or [OrbStack](https://orbstack.dev/))
- [`golang-migrate`](https://github.com/golang-migrate/migrate) CLI — used to apply database schema migrations

```bash
# macOS
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

### 2. Start the database

```bash
docker compose up -d db
```

### 3. Run migrations

```bash
migrate -path internal/store/migrations -database "postgres://fplbot:password@localhost:5432/fplbanterbot?sslmode=disable" up
```

This only needs to be run once per fresh database, or when new migration files are added.

### 4. Run the bot

```bash
go run cmd/bot/main.go
```

## Configuration

All configuration is via environment variables. See [`.env.example`](.env.example) for the full list.

| Variable | Required | Description |
|----------|----------|-------------|
| `FPL_LEAGUE_ID` | Yes | Your FPL league ID |
| `FPL_LEAGUE_TYPE` | No | `h2h` or `classic` (default: `h2h`) |
| `TELEGRAM_BOT_TOKEN` | Yes | Token from [@BotFather](https://t.me/BotFather) |
| `TELEGRAM_CHAT_ID` | Yes | Target group chat ID |
| `DATABASE_URL` | Yes | Postgres connection string |
| `LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |

## Project structure

```
cmd/bot/             Entrypoint — wires everything together
internal/config/     Environment variable loading + validation
internal/fpl/        FPL HTTP client + API response types
internal/poller/     Gameweek lifecycle state machine
internal/stats/      Diff engine + alert detection
internal/store/      Database interface + Postgres implementation
pkg/notify/          Notifier interface (public API for chat platforms)
pkg/notify/telegram/ Telegram implementation
```

`internal/` packages are private to this module (compiler-enforced). `pkg/` is the public API — import `pkg/notify` to build your own chat platform adapter.

## Adding a new chat platform

Implement the `Notifier` interface in [`pkg/notify/notify.go`](pkg/notify/notify.go):

```go
type Notifier interface {
    SendAlerts(ctx context.Context, alerts []Alert) error
}
```

See `pkg/notify/telegram/` for a reference implementation.

## Development

```bash
# Run tests
go test ./...

# Run tests for a specific package
go test ./internal/fpl/ -v

# Lint
golangci-lint run

# Build Docker image (ARM for Raspberry Pi)
docker build --platform linux/arm64 -t fpl-banter-bot .
```

### Database management

```bash
# Start the dev database
docker compose up -d db

# Check container status
docker compose ps

# View database logs
docker compose logs db

# Run migrations
migrate -path internal/store/migrations -database "postgres://fplbot:password@localhost:5432/fplbanterbot?sslmode=disable" up

# Roll back the last migration
migrate -path internal/store/migrations -database "postgres://fplbot:password@localhost:5432/fplbanterbot?sslmode=disable" down 1

# Connect to the database directly
docker exec -it fpl-banter-bot-db-1 psql -U fplbot -d fplbanterbot

# Stop the database (data is preserved)
docker compose down

# Stop the database and delete all data (fresh start)
docker compose down -v
```

## License

[MIT](LICENSE)
