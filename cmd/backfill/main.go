// Package main is the entrypoint for the backfill command.
//
// Go idiom — MULTIPLE BINARIES UNDER cmd/:
//
// Go projects commonly have multiple main packages under cmd/. Each gets
// its own directory and main.go, compiled into a separate binary. They
// share internal/ packages — the backfill command reuses the same config,
// FPL client, store, and poller packages as the bot.
//
// This keeps the bot's Run() loop clean (no one-time recovery logic) and
// makes backfill an explicit, intentional operation rather than something
// that runs automatically on every startup.
//
// Usage:
//
//	make backfill
//	# or: go run cmd/backfill/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/config"
	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/poller"
	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// Load configuration — same env vars as the bot.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := config.SetupLogger(cfg.LogLevel, cfg.LogFormat)
	mainLog := logger.With("component", "main", "league_id", cfg.FPLLeagueID)
	fplLog := logger.With("component", "fpl")
	storeLog := logger.With("component", "store")
	pollerLog := logger.With("component", "poller", "league_id", cfg.FPLLeagueID)

	mainLog.Info("starting backfill",
		"league_id", cfg.FPLLeagueID,
		"league_type", cfg.FPLLeagueType,
	)

	// Signal-based graceful cancellation — pressing Ctrl+C mid-backfill
	// cancels the context, which the backfill loop checks between GWs.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Database connection pool.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		mainLog.Error("failed to create database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		mainLog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	mainLog.Info("connected to database")

	// Run migrations (same embedded SQL as the bot).
	if err := store.RunMigrations(cfg.DatabaseURL); err != nil {
		mainLog.Error("failed to run database migrations", "error", err)
		os.Exit(1)
	}
	mainLog.Info("database migrations complete")

	// FPL API client.
	fplClient := fpl.NewClient("https://fantasy.premierleague.com/api", &http.Client{
		Timeout: 30 * time.Second,
	}, fplLog)

	// Store + poller — same wiring as cmd/bot/main.go.
	appStore := store.New(pool, storeLog)
	statsEngine := stats.New(appStore, int64(cfg.FPLLeagueID))
	pollerCfg := poller.Config{
		LeagueID:           cfg.FPLLeagueID,
		LeagueType:         cfg.FPLLeagueType,
		IdleInterval:       time.Duration(cfg.PollIdleInterval) * time.Second,
		LiveInterval:       time.Duration(cfg.PollLiveInterval) * time.Second,
		ProcessingInterval: time.Duration(cfg.PollProcessingInterval) * time.Second,
	}

	onBackfillEvent := func(ctx context.Context, eventID int) error {
		if _, err := statsEngine.BuildGameweekAlerts(ctx, eventID); err != nil {
			return fmt.Errorf("build alerts for event %d: %w", eventID, err)
		}
		return nil
	}

	p, err := poller.New(fplClient, appStore, pollerCfg, onBackfillEvent, pollerLog)
	if err != nil {
		mainLog.Error("failed to create poller", "error", err)
		os.Exit(1)
	}

	// Run backfill — this blocks until complete or context cancellation.
	if err := p.Backfill(ctx); err != nil {
		if errors.Is(err, fpl.ErrGameUpdating) {
			mainLog.Warn("backfill paused because the FPL API is temporarily updating; try again once the game is available",
				"error", err,
			)
			os.Exit(1)
		}
		mainLog.Error("backfill failed", "error", err)
		os.Exit(1)
	}

	mainLog.Info("backfill finished successfully")
}
