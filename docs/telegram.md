# Telegram Setup

This guide covers everything needed to connect fpl-banter-bot to a Telegram group chat.

## Creating a Bot

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts to choose a name and username
3. BotFather replies with a **bot token** — save this as `TELEGRAM_BOT_TOKEN`
4. Add the bot to your group chat
5. Get the group's **chat ID** (see below) and save it as `TELEGRAM_CHAT_ID`

### Finding the Chat ID

After adding the bot to the group, send a message in the group, then run:

```bash
curl -s "https://api.telegram.org/bot<YOUR_TOKEN>/getUpdates" | jq '.result[0].message.chat.id'
```

Group chat IDs are negative numbers (e.g., `-1001234567890`).

## Bot Commands

The bot registers these commands with Telegram on startup via the [`setMyCommands`](https://core.telegram.org/bots/api#setmycommands) API. Users see them as autocomplete suggestions when typing `/` in the chat.

| Command | Description |
|---------|-------------|
| `/standings` | Current league standings table |
| `/streak` | Active win and loss streaks |
| `/history` | H2H record between two managers |
| `/deadline` | Next gameweek deadline |

### How Command Registration Works

- Commands are registered automatically on every startup — no manual BotFather step needed
- Commands are **scoped to the configured chat** (`TELEGRAM_CHAT_ID`), so they won't appear in other chats where the bot would silently ignore messages
- Any previously-set global (default-scope) commands are cleared on startup
- The command list is defined in `internal/bot/commands.go` as a single source of truth

### Adding a New Command

1. Add a constant in `internal/bot/commands.go` (e.g., `CmdNewCmd = "newcmd"`)
2. Add an entry to the `Commands` slice with its description
3. Add a `case "/" + CmdNewCmd:` in `dispatchCommand` (`internal/bot/bot.go`)
4. Implement the handler function
5. The `TestCommands_MatchesDispatch` test will catch any mismatch between the metadata and the router

## Webhook Configuration

The bot receives Telegram updates via a webhook — Telegram POSTs updates to a public HTTPS URL that you provide. The bot registers the webhook on startup and deregisters it on graceful shutdown.

### Required Environment Variables

| Variable | Description |
|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | Target group chat ID (negative for groups) |
| `WEBHOOK_BASE_URL` | Publicly reachable HTTPS URL (no trailing slash) |
| `WEBHOOK_PORT` | Local HTTP server port (default: `8080`) |
| `WEBHOOK_SECRET` | Secret path segment for webhook URL (auto-generated if omitted) |

The webhook URL is constructed as: `{WEBHOOK_BASE_URL}/webhook/{WEBHOOK_SECRET}`

### Security

- The webhook path includes a secret token — any request to a different path gets an automatic 404
- Only updates from `TELEGRAM_CHAT_ID` are processed; other chats are silently ignored
- Request bodies are capped at 1MB to prevent abuse

## Tailscale Funnel (Recommended)

[Tailscale Funnel](https://tailscale.com/kb/1223/funnel) exposes a local port to the public internet over HTTPS — no port forwarding, no dynamic DNS, no certificate management.

### Prerequisites

- Tailscale installed via **standalone installer** (not the macOS App Store version, which lacks Funnel support)
- MagicDNS and HTTPS certificates enabled in the Tailscale admin console

### Important: Machine Naming

Rename your machine **before** enabling HTTPS certificates. Machine names are recorded in [Certificate Transparency](https://certificate.transparency.dev/) logs and cannot be changed after certificate issuance. Choose a name that doesn't reveal personal information.

```bash
sudo tailscale set --hostname=your-chosen-name
```

### Starting Funnel

```bash
sudo tailscale funnel --bg 8080
```

This routes `https://your-chosen-name.tailXXXXX.ts.net` to `localhost:8080`.

### Environment Configuration

```bash
WEBHOOK_BASE_URL=https://your-chosen-name.tailXXXXX.ts.net
WEBHOOK_PORT=8080
```

## Data-Collection-Only Mode

If both `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` are omitted, the bot runs in data-collection-only mode: it polls the FPL API and persists data to Postgres, but sends no notifications and starts no webhook server. This is useful for current-season backfill/enrichment or running the bot without Telegram.
