// Package main is the entrypoint for the fpl-banter-bot.
// In Go, the combination of "package main" + "func main()" defines an
// executable program (as opposed to a library package).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/config"
	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/poller"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
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
	// TODO: wire stats engine and notifier when cfg.TelegramConfigured is true.
	//
	// Go pattern — IMPLICIT INTERFACE SATISFACTION:
	// *fpl.Client satisfies poller.FPLClient, and *store.PostgresStore
	// satisfies store.Store — no cast or "implements" keyword needed.
	// This is Go's structural typing in action.
	var onFinalized poller.OnGameweekFinalized

	p, err := poller.New(fplClient, appStore, pollerCfg, onFinalized)
	if err != nil {
		slog.Error("failed to create poller", "error", err)
		os.Exit(1)
	}

	// Run the poller. This blocks until the context is cancelled (SIGINT
	// or SIGTERM). The poller follows the http.ListenAndServe convention:
	// Run() blocks, and the caller (us) controls lifecycle.
	slog.Info("starting poller")
	if err := p.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("poller exited with error", "error", err)
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
