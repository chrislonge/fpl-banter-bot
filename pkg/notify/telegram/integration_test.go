package telegram

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// TestIntegration_SendAlerts sends a real message to a real Telegram chat.
//
// This test is gated by environment variables — it skips cleanly when
// credentials are absent, so `go test ./...` always passes. Run it
// explicitly with `make test-telegram` when you want to verify that
// your bot token and chat ID are working.
//
// Go pattern — ENV-VAR-GATED INTEGRATION TESTS:
//
// This follows the same pattern as the store integration tests
// (STORE_TEST_DATABASE_URL). The convention is: if the env var is
// missing, skip with a helpful message rather than failing. This lets
// CI and local `go test ./...` run clean while still supporting
// real-world verification when you opt in.
func TestIntegration_SendAlerts(t *testing.T) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")

	if token == "" || chatID == "" {
		t.Skip("skipping integration test: TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must be set")
	}

	// Use event ID 99 so test messages are visually distinct from real
	// bot output. Your league almost certainly doesn't have 99 gameweeks.
	const testEventID = 99
	const testLeagueID = 0

	// Build a representative set of alerts covering every alert kind.
	// These reuse the fixture builders from format_test.go — accessible
	// here because we're in the same package (white-box test).
	alerts := []notify.Alert{
		awardsAlert(testEventID, testLeagueID, &notify.GameweekAwardsAlert{
			ManagerOfTheWeek: &notify.ManagerScore{Manager: managerRef(1, "Alice"), Score: 72},
			WoodenSpoon:      &notify.ManagerScore{Manager: managerRef(4, "Dave"), Score: 38},
			CaptainGenius: &notify.CaptainAwardAlert{
				Manager:           managerRef(1, "Alice"),
				Captain:           notify.PlayerRef{ElementID: 10, Name: "Salah"},
				CaptainPoints:     12,
				CaptainMultiplier: 2,
				TotalPoints:       24,
			},
			BenchWarmer: &notify.BenchWarmerAwardAlert{
				Manager:       managerRef(3, "Charlie"),
				PointsOnBench: 15,
			},
			BiggestThrashing: &notify.MatchupAwardAlert{
				Winner:      managerRef(1, "Alice"),
				WinnerScore: 72,
				Loser:       managerRef(2, "Bob"),
				LoserScore:  45,
				Margin:      27,
			},
			LuckiestWin: &notify.MatchupAwardAlert{
				Winner:      managerRef(4, "Dave"),
				WinnerScore: 40,
				Loser:       managerRef(5, "Eve"),
				LoserScore:  39,
				Margin:      1,
			},
			PlotTwist: &notify.UpsetAlert{
				Winner:             managerRef(4, "Dave"),
				WinnerScore:        55,
				WinnerPreviousRank: 5,
				Loser:              managerRef(2, "Bob"),
				LoserScore:         45,
				LoserPreviousRank:  2,
				RankGap:            3,
			},
		}),
		h2hAlert(testEventID, testLeagueID,
			managerRef(1, "Alice"), 72,
			managerRef(2, "Bob"), 45,
			int64Ptr(1),
		),
		h2hAlert(testEventID, testLeagueID,
			managerRef(3, "Charlie"), 55,
			managerRef(4, "Dave"), 55,
			nil, // draw
		),
		rankAlert(testEventID, testLeagueID, managerRef(1, "Alice"), 3, 1, true),
		rankAlert(testEventID, testLeagueID, managerRef(2, "Bob"), 2, 4, false),
		streakAlert(testEventID, testLeagueID, managerRef(1, "Alice"), notify.StreakKindWin, 5, 95, 99),
		streakAlert(testEventID, testLeagueID, managerRef(4, "Dave"), notify.StreakKindLoss, 3, 97, 99),
		chipAlert(testEventID, testLeagueID, managerRef(3, "Charlie"), "wildcard"),
	}

	client := New(
		&http.Client{Timeout: 10 * time.Second},
		token,
		chatID,
	)

	err := client.SendAlerts(context.Background(), alerts)
	if err != nil {
		t.Fatalf("SendAlerts to real Telegram chat failed: %v", err)
	}

	t.Log("message sent successfully — check your Telegram group chat")
}
