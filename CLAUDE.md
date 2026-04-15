# CLAUDE.md — Project Context for Claude Code

## About This Project

fpl-banter-bot is a Go service that tracks a Fantasy Premier League head-to-head league and announces banter-worthy stats to a group chat. It polls the unofficial FPL API, detects interesting events (rank changes, win streaks, chip usage), and sends formatted messages proactively after each gameweek.

The project is open source, designed for multi-tenancy, and structured to support multiple chat platforms via a Notifier interface.

## Tech Stack

- **Language**: Go (latest stable)
- **Database**: PostgreSQL (latest stable, containerized via `postgres:XX-alpine`)
- **Database driver**: `pgx` (NOT `lib/pq`)
- **Migrations**: `golang-migrate`
- **Telegram**: `go-telegram-bot-api` or direct Bot API calls via `net/http`
- **Containerization**: Docker Compose (Phase 1-2), k3s (Phase 3)
- **CI/CD**: GitHub Actions (build ARM image, push to GHCR)
- **Logging**: `slog` (Go stdlib structured logging)
- **Target hardware**: Raspberry Pi 4 or 5 (ARM64)

## Architecture Rules

1. **Every table has `league_id`**. This enables multi-tenancy without schema changes.
2. **The Notifier interface lives in `pkg/notify/`**. Platform implementations (Telegram, Discord) go in subdirectories. The stats engine never imports a platform-specific package.
3. **The FPL client is in `internal/fpl/`**. It parses raw JSON into typed Go structs at the boundary. Downstream code never works with `map[string]interface{}`.
4. **Configuration is via environment variables**. No hardcoded league IDs, tokens, or connection strings. Use the `config` package to read and validate env vars at startup. Telegram credentials are optional — if absent, the bot runs in data-collection-only mode.
5. **Composite primary keys for idempotency**. The schema uses compound keys (e.g., `league_id, event_id, manager_id`) so duplicate inserts can use `ON CONFLICT` safely.

## Project Structure

```
fpl-banter-bot/
├── cmd/bot/main.go              # Entrypoint
├── internal/
│   ├── config/                  # Env var parsing
│   ├── fpl/                     # FPL HTTP client + models
│   ├── poller/                  # Gameweek state machine
│   ├── stats/                   # Diff engine + alert types
│   └── store/                   # DB interface + Postgres impl + migrations
├── pkg/notify/                  # Notifier interface (public API)
│   └── telegram/                # Telegram implementation
├── docker-compose.yml
├── Dockerfile
├── .env.example
└── go.mod
```

`internal/` = private to this module (Go compiler enforces this).
`pkg/` = public API for contributors and future importers.

## Key Design Patterns

- **Adaptive Polling State Machine**: The poller transitions through IDLE → LIVE → PROCESSING → FINALIZED states with variable poll intervals. The `event-status` FPL endpoint drives transitions.
- **Callback-based decoupling**: The poller doesn't know about the stats engine. It calls a function when a gameweek finalizes. The stats engine doesn't know about Telegram. It calls methods on a Notifier interface.
- **Snapshot-and-diff**: After each gameweek, we snapshot standings to Postgres. The stats engine diffs the current snapshot against the previous one to detect rank changes, streaks, etc.

## FPL API Quick Reference

Base: `https://fantasy.premierleague.com/api/`

| Endpoint | Auth | Returns |
|----------|------|---------|
| `bootstrap-static/` | No | All gameweeks, teams, players (~1.3MB, cache it) |
| `event-status/` | No | Bonus/league processing status (the heartbeat) |
| `leagues-h2h/{id}/standings/` | No | H2H league standings |
| `entry/{id}/history/` | No | Manager GW history + chip usage |
| `entry/{id}/event/{gw}/picks/` | No | Manager's team picks for a GW |

No authentication required. CORS restricted (server-side only).

## Development Commands

```bash
# Run locally
make db-up          # or: docker compose up -d db
make run            # or: go run cmd/bot/main.go

# Test
make test           # or: go test ./...
make test-store     # store integration tests (requires Postgres)
make test-all       # all tests including store integration

# Lint
make lint           # or: golangci-lint run

# Build Docker image (ARM for Pi)
docker build --platform linux/arm64 -t fpl-banter-bot .

# Database management
make db-up          # start Postgres
make db-down        # stop Postgres (data preserved)
make db-reset       # destroy + recreate (needed after schema changes)
```

Migrations run automatically on bot startup via embedded SQL files. The `golang-migrate` CLI is optional (manual rollbacks only).

### Release Process

```bash
make release VERSION=0.6.0   # runs lint + test, bumps docker-compose.yml, commits, tags, and pushes
```

- **Patch releases** (`0.5.0` → `0.5.1`): commit the fix, then `make release VERSION=0.5.1` — `docker-compose.yml` does not need changing since `0.5` already tracks the latest patch
- **Minor/major releases**: `make release` bumps the `major.minor` tag in `docker-compose.yml` automatically
- CI builds the ARM64 image and pushes to GHCR on every `v*` tag push
- Tag pushes are protected by a GitHub ruleset — only the repo owner can push `v*` tags

## Environment Variables

See `.env.example` for the full list. Copy to `.env` for local values (gitignored). The Makefile loads `.env` automatically.

- `TELEGRAM_BOT_TOKEN` — From @BotFather
- `TELEGRAM_CHAT_ID` — Target group chat ID
- `DATABASE_URL` — Postgres connection string
- `STORE_TEST_DATABASE_URL` — Test database connection string (for `make test-store`)
- `FPL_LEAGUE_ID` — FPL league ID
- `FPL_LEAGUE_TYPE` — `h2h` or `classic`

## After Implementing a Plan

Update `LEARNING_JOURNAL.md` at the project root with any new Go patterns, idioms, or architectural concepts encountered during the implementation. Add a section for the phase, with pattern names, code examples from this project, explanations of why they matter, and analogies to Swift/Kotlin where helpful. Concepts that may be new to the user (like "data provenance") should be explained from first principles.

## Contributing

- The `Notifier` interface in `pkg/notify/` is the extension point for new chat platforms
- All tables have `league_id` — the schema supports multi-league from day one
- Use `.env.example` as a template; never commit `.env`
- Run `go test ./...` before submitting a PR