// Package main is a one-shot diagnostic tool that exercises the full
// stats → notify pipeline against real data already in the database.
//
// This is an operational tool, not an assertion-based test. You run it
// interactively to preview what the bot would send for a given gameweek,
// then visually verify the Telegram messages.
//
// Usage:
//
//	make notify-test              # uses the latest event ID in the DB
//	make notify-test GW=12        # targets a specific gameweek
//	make notify-test DRY_RUN=1    # preview alerts without sending
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/config"
	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dryRun := flag.Bool("dry-run", os.Getenv("DRY_RUN") == "1", "build and log alerts without sending to Telegram")
	verify := flag.Bool("verify", os.Getenv("VERIFY") == "1", "scan stored gameweeks and print a verification report instead of targeting a single gameweek")
	verifyLast := flag.Int("verify-last", envInt("VERIFY_LAST", 0), "limit verification to the most recent N stored gameweeks (0 = all)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	setupLogger(cfg.LogLevel)

	if !cfg.TelegramConfigured && !*dryRun {
		slog.Error("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must be set (or use --dry-run)")
		os.Exit(1)
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to create database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		slog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to database")

	if err := store.RunMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("failed to run database migrations", "error", err)
		os.Exit(1)
	}

	appStore := store.New(pool)
	fplClient := fpl.NewClient("https://fantasy.premierleague.com/api", &http.Client{
		Timeout: 30 * time.Second,
	})
	playerLookup := newPlayerLookup(fplClient)
	statsEngine := stats.NewWithPlayerLookup(appStore, int64(cfg.FPLLeagueID), playerLookup)

	if *verify {
		if err := runVerification(ctx, appStore, statsEngine, int64(cfg.FPLLeagueID), *verifyLast); err != nil {
			slog.Error("verification failed", "error", err)
			os.Exit(1)
		}
		return
	}

	eventID, err := resolveEventID(ctx, appStore, int64(cfg.FPLLeagueID))
	if err != nil {
		slog.Error("failed to resolve event ID", "error", err)
		os.Exit(1)
	}
	slog.Info("targeting event", "event_id", eventID)

	alerts, err := statsEngine.BuildGameweekAlerts(ctx, eventID)
	if err != nil {
		slog.Error("failed to build alerts", "error", err)
		os.Exit(1)
	}

	slog.Info("alerts built", "count", len(alerts))
	for i, a := range alerts {
		slog.Info(fmt.Sprintf("alert[%d]", i), "kind", a.Kind)
	}

	if len(alerts) == 0 {
		slog.Warn("no alerts generated — check that the event has standings, h2h results, and snapshot_meta with 'historical' fidelity")
		os.Exit(0)
	}

	if *dryRun {
		slog.Info("dry run — skipping Telegram delivery and previewing the awards-first Telegram recap",
			"event_id", eventID,
			"alert_count", len(alerts),
		)

		// Preview the formatted messages so you can see the exact output.
		messages, err := telegram.FormatAlerts(alerts)
		if err != nil {
			slog.Error("failed to format alerts", "error", err)
			os.Exit(1)
		}
		for i, msg := range messages {
			fmt.Printf("\n--- Message %d ---\n%s\n", i+1, msg)
		}
		return
	}

	notifier := telegram.New(
		&http.Client{Timeout: 10 * time.Second},
		cfg.TelegramBotToken,
		cfg.TelegramChatID,
	)

	if err := notifier.SendAlerts(ctx, alerts); err != nil {
		slog.Error("failed to send alerts", "error", err)
		os.Exit(1)
	}

	slog.Info("alerts sent successfully", "event_id", eventID, "count", len(alerts))
}

// resolveEventID returns the event ID from the GW env var if set,
// otherwise falls back to the latest event stored for this league.
func resolveEventID(ctx context.Context, appStore store.Store, leagueID int64) (int, error) {
	if gwStr := os.Getenv("GW"); gwStr != "" {
		gw, err := strconv.Atoi(gwStr)
		if err != nil || gw < 1 {
			return 0, fmt.Errorf("GW=%q is not a valid gameweek number", gwStr)
		}
		return gw, nil
	}

	eventID, err := appStore.GetLatestEventID(ctx, leagueID)
	if err != nil {
		return 0, fmt.Errorf("get latest event id: %w", err)
	}
	if eventID == 0 {
		return 0, fmt.Errorf("no events found in database for league %d — run make backfill first", leagueID)
	}
	return eventID, nil
}

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
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
