// Package main is the entrypoint for the fpl-banter-bot.
// In Go, the combination of "package main" + "func main()" defines an
// executable program (as opposed to a library package).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/chrislonge/fpl-banter-bot/internal/bot"
	"github.com/chrislonge/fpl-banter-bot/internal/config"
	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/poller"
	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// Load configuration from environment variables.
	cfg, err := config.Load()
	if err != nil {
		// slog.Error logs at ERROR level with structured key-value pairs.
		// In Go, the convention is: check the error immediately after the
		// call, handle it, and return (or exit). No deeply nested try/catch.
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Configure the structured logger based on the configured log level.
	setupLogger(cfg.LogLevel)

	slog.Info("starting fpl-banter-bot",
		"league_id", cfg.FPLLeagueID,
		"league_type", cfg.FPLLeagueType,
	)

	if cfg.TelegramConfigured {
		slog.Info("telegram credentials configured", "platform", "telegram")
	} else {
		slog.Info("running in data-collection-only mode (no notification credentials configured)")
	}

	// Signal-based graceful shutdown.
	//
	// Go pattern — signal.NotifyContext:
	//
	// Creates a context that cancels automatically when the process
	// receives SIGINT (Ctrl+C) or SIGTERM (Docker stop / kill signal).
	// This is the idiomatic way to wire OS signals into Go's context
	// system. When the signal fires, ctx.Done() closes, and any
	// blocking select on ctx.Done() (like the poller's Run loop)
	// returns immediately — no waiting for timers to expire.
	//
	// The stop() function unregisters the signal handler, restoring
	// default OS behavior. We defer it so cleanup happens on exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// pgxpool.New creates a connection pool. A pool maintains several
	// open DB connections and reuses them, avoiding the overhead of
	// connect/disconnect on every query. This is the standard approach
	// for any long-running service.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to create database pool", "error", err)
		os.Exit(1)
	}
	// defer schedules pool.Close() to run when main() returns.
	// Always defer cleanup immediately after acquiring a resource.
	defer pool.Close()

	// Ping verifies the database is reachable. This is a fast sanity
	// check — fail fast on startup rather than discovering the DB is
	// down when the first gameweek finalizes.
	if err := pool.Ping(ctx); err != nil {
		slog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}

	slog.Info("connected to database")

	// Create the FPL API client. We pass a custom *http.Client with an
	// explicit timeout rather than relying on the default (which has no
	// timeout at all!). Without a timeout, a hung FPL server could block
	// the bot forever.
	//
	// This follows the same Dependency Injection pattern as pgxpool above:
	// we configure the transport externally and pass it in.
	fplClient := fpl.NewClient("https://fantasy.premierleague.com/api", &http.Client{
		Timeout: 30 * time.Second,
	})

	// Smoke-test the FPL API on startup, same "fail fast" pattern as the
	// database ping. If the API is unreachable, we want to know immediately
	// rather than discovering it when the first gameweek finalizes.
	status, err := fplClient.GetEventStatus(ctx)
	if err != nil {
		slog.Error("failed to reach FPL API", "error", err)
		os.Exit(1)
	}
	slog.Info("FPL API reachable", "leagues_status", status.Leagues)

	// Run database migrations. The SQL files are embedded in the binary
	// via //go:embed, so there are no external files to deploy.
	if err := store.RunMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("failed to run database migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("database migrations complete")

	// Create the store — the persistence layer between the FPL client
	// and the future stats engine/poller.
	appStore := store.New(pool)

	// Configure the poller — the adaptive polling state machine that
	// drives the bot's gameweek lifecycle.
	//
	// The Config converts seconds (from env vars) to time.Duration.
	// New() validates the config — it fails fast if LeagueType != "h2h".
	pollerCfg := poller.Config{
		LeagueID:           cfg.FPLLeagueID,
		LeagueType:         cfg.FPLLeagueType,
		IdleInterval:       time.Duration(cfg.PollIdleInterval) * time.Second,
		LiveInterval:       time.Duration(cfg.PollLiveInterval) * time.Second,
		ProcessingInterval: time.Duration(cfg.PollProcessingInterval) * time.Second,
	}

	// Wire the notification pipeline when Telegram is configured.
	// In data-collection-only mode, onFinalized stays nil and the poller
	// collects + persists data without sending any notifications.
	//
	// Go pattern — CLOSURE AS GLUE:
	//
	// The closure below captures statsEngine and tgNotifier, connecting
	// three packages (poller, stats, telegram) that never import each
	// other. This is the idiomatic Go alternative to a DI framework —
	// explicit, readable, zero magic. The poller calls onFinalized when
	// a gameweek is fully persisted; the closure orchestrates stats
	// computation and notification delivery.
	//
	// Go pattern — IMPLICIT INTERFACE SATISFACTION:
	// *fpl.Client satisfies poller.FPLClient, *store.PostgresStore
	// satisfies both store.Store and stats.Store — no cast or
	// "implements" keyword needed. This is Go's structural typing.
	var onFinalized poller.OnGameweekFinalized

	// Hoist statsEngine and tgNotifier to outer scope so they're available
	// for both the proactive pipeline (onFinalized) and the reactive bot
	// server (bot.New). In the previous phase, these were scoped inside the
	// if-block; now the bot server needs them too.
	var statsEngine *stats.Engine
	var tgNotifier *telegram.Client

	if cfg.TelegramConfigured {
		statsEngine = stats.New(appStore, int64(cfg.FPLLeagueID))
		tgNotifier = telegram.New(
			&http.Client{Timeout: 10 * time.Second},
			cfg.TelegramBotToken,
			cfg.TelegramChatID,
		)

		onFinalized = func(ctx context.Context, eventID int) error {
			alerts, err := statsEngine.BuildGameweekAlerts(ctx, eventID)
			if err != nil {
				return fmt.Errorf("build alerts for event %d: %w", eventID, err)
			}
			if len(alerts) == 0 {
				slog.Info("no alerts to send", "event_id", eventID)
				return nil
			}
			slog.Info("sending alerts", "event_id", eventID, "alert_count", len(alerts))
			return tgNotifier.SendAlerts(ctx, alerts)
		}

		// Register bot commands scoped to the configured chat.
		// Using chat scope prevents commands from appearing in other chats
		// where the bot would silently ignore them (see handleUpdate chat ID filter).
		tgCmds := make([]telegram.BotCommand, len(bot.Commands))
		for i, cmd := range bot.Commands {
			tgCmds[i] = telegram.BotCommand{
				Command:     cmd.Name,
				Description: cmd.Description,
			}
		}
		chatID, err := strconv.ParseInt(cfg.TelegramChatID, 10, 64)
		if err != nil {
			slog.Error("invalid TELEGRAM_CHAT_ID for command registration", "error", err)
			os.Exit(1)
		}
		chatScope := &telegram.BotCommandScope{Type: "chat", ChatID: chatID}

		// Clear any previously-set default-scope commands so they don't
		// leak into other chats.
		if err := tgNotifier.DeleteMyCommands(ctx, nil); err != nil {
			slog.Warn("failed to clear default-scope commands", "error", err)
		}
		if err := tgNotifier.SetMyCommands(ctx, tgCmds, chatScope); err != nil {
			slog.Warn("failed to register bot commands", "error", err)
		}

		slog.Info("notification pipeline wired", "platform", "telegram")
	}

	p, err := poller.New(fplClient, appStore, pollerCfg, onFinalized)
	if err != nil {
		slog.Error("failed to create poller", "error", err)
		os.Exit(1)
	}

	// Go pattern — errgroup STRUCTURED CONCURRENCY:
	//
	// errgroup.WithContext creates a group of goroutines that share a derived
	// context (gctx). Key properties:
	//
	//   1. First error cancels gctx — if the webhook server crashes, the
	//      poller's ctx.Done() fires and it shuts down (and vice versa).
	//   2. g.Wait() blocks until ALL goroutines return, collecting the
	//      first non-nil error.
	//   3. No goroutine outlives its spawner — structured concurrency
	//      guarantees cleanup.
	//
	// This is the Go equivalent of Swift's TaskGroup or Kotlin's
	// coroutineScope { launch { ... }; launch { ... } }. The semantics
	// are identical: first failure cancels the group, Wait/awaitAll
	// collects the result.
	//
	// Compare to the previous pattern (p.Run(ctx) blocking main):
	// that worked for one goroutine, but with two (poller + webhook server),
	// we need structured concurrency to ensure clean shutdown of both.
	g, gctx := errgroup.WithContext(ctx)

	slog.Info("starting poller")
	g.Go(func() error { return p.Run(gctx) })

	if cfg.TelegramConfigured {
		slog.Info("webhook configured", "base_url", cfg.WebhookBaseURL)

		// Wire the bot server with all its dependencies.
		//
		// *telegram.Client satisfies bot.TelegramBot (SendRaw, SetWebhook,
		// DeleteWebhook). *stats.Engine satisfies bot.StatsQuerier.
		// *store.PostgresStore satisfies bot.LeagueStore. *fpl.Client
		// satisfies bot.FPLQuerier. *poller.Poller satisfies
		// bot.PollerStatusProvider. All via implicit structural typing.
		botServer := bot.New(
			tgNotifier,  // satisfies bot.TelegramBot
			statsEngine, // satisfies bot.StatsQuerier
			appStore,    // satisfies bot.LeagueStore
			fplClient,   // satisfies bot.FPLQuerier
			p,           // satisfies bot.PollerStatusProvider
			bot.Config{
				LeagueID:       int64(cfg.FPLLeagueID),
				ChatID:         cfg.TelegramChatID,
				Port:           cfg.WebhookPort,
				WebhookBaseURL:   cfg.WebhookBaseURL,
				WebhookSecret:    cfg.WebhookSecret,
				DeadlineTimezone: cfg.DeadlineTimezone,
			},
		)

		g.Go(func() error { return botServer.RunServer(gctx) })
	}

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("bot exited with error", "error", err)
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}

// setupLogger configures the global slog logger with the given level.
// slog is Go's stdlib structured logger. It outputs key-value pairs
// that are easy to search and filter in production.
func setupLogger(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))
}
