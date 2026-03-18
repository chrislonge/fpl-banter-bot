// ---------------------------------------------------------------------------
// Live API tests — manual validation against the real FPL API.
//
// These tests are SKIPPED by default. They hit the real FPL servers over
// the internet, so they:
//   - Require network access
//   - Are slower than unit tests
//   - Could fail if the FPL API is down or changes its format
//
// Run them manually when you want to validate that your structs still
// match the real API responses:
//
//	FPL_LIVE_TEST=1 go test ./internal/fpl/ -run TestLiveAPI -v
//
// Go concept — t.Skip():
//
// t.Skip() marks a test as "skipped" (not failed). It shows up as SKIP
// in the output. This is the standard Go pattern for tests that depend
// on external services. They don't run in CI by default, and developers
// opt in locally by setting an environment variable.
//
// Go concept — context.WithTimeout:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
//	defer cancel()
//
// This creates a context that automatically cancels after 15 seconds.
// If the FPL API is slow or unreachable, the HTTP request will abort
// after the timeout instead of hanging forever. The `defer cancel()`
// ensures resources are freed even if the request completes quickly.
// Always defer cancel() — the Go linter (and go vet) will warn if you don't.
// ---------------------------------------------------------------------------
package fpl_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
)

const (
	liveBaseURL = "https://fantasy.premierleague.com/api"
	liveTimeout = 15 * time.Second
)

// skipUnlessLive skips the test unless the FPL_LIVE_TEST env var is set.
// This helper avoids repeating the same check in every test function.
func skipUnlessLive(t *testing.T) {
	t.Helper() // Marks this as a test helper — errors report the caller's line, not this line.
	if os.Getenv("FPL_LIVE_TEST") != "1" {
		t.Skip("set FPL_LIVE_TEST=1 to run live API tests")
	}
}

func TestLiveAPI_Bootstrap(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	client := fpl.NewClient(liveBaseURL, nil)
	resp, err := client.GetBootstrap(ctx)
	if err != nil {
		t.Fatalf("GetBootstrap: %v", err)
	}

	t.Logf("Events: %d, Teams: %d", len(resp.Events), len(resp.Teams))

	// Sanity checks — the Premier League always has 38 gameweeks and 20 teams.
	if len(resp.Events) < 38 {
		t.Errorf("expected at least 38 events, got %d", len(resp.Events))
	}
	if len(resp.Teams) != 20 {
		t.Errorf("expected 20 teams, got %d", len(resp.Teams))
	}

	// Log a sample event to visually inspect the field mapping.
	if len(resp.Events) > 0 {
		ev := resp.Events[0]
		t.Logf("First event: ID=%d Name=%q Finished=%v DeadlineTime=%q",
			ev.ID, ev.Name, ev.Finished, ev.DeadlineTime)
	}
}

func TestLiveAPI_EventStatus(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	client := fpl.NewClient(liveBaseURL, nil)
	resp, err := client.GetEventStatus(ctx)
	if err != nil {
		t.Fatalf("GetEventStatus: %v", err)
	}

	t.Logf("Leagues: %q, Status entries: %d", resp.Leagues, len(resp.Status))

	for i, s := range resp.Status {
		t.Logf("  Status[%d]: Event=%d Date=%s BonusAdded=%v Points=%q",
			i, s.Event, s.Date, s.BonusAdded, s.Points)
	}
}

func TestLiveAPI_H2HStandings(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	client := fpl.NewClient(liveBaseURL, nil)

	// Use the Capital FC league from the project plan.
	resp, err := client.GetAllH2HStandings(ctx, 916670)
	if err != nil {
		t.Fatalf("GetAllH2HStandings: %v", err)
	}

	t.Logf("League: %q (ID=%d), Managers: %d",
		resp.League.Name, resp.League.ID, len(resp.Standings.Results))

	for _, e := range resp.Standings.Results {
		t.Logf("  #%d %s (%s) — W:%d D:%d L:%d Total:%d",
			e.Rank, e.PlayerName, e.EntryName,
			e.MatchesWon, e.MatchesDrawn, e.MatchesLost, e.Total)
	}
}

func TestLiveAPI_ManagerHistory(t *testing.T) {
	skipUnlessLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	client := fpl.NewClient(liveBaseURL, nil)

	// First, fetch the league to get a real manager ID.
	standings, err := client.GetH2HStandings(ctx, 916670, 1)
	if err != nil {
		t.Fatalf("GetH2HStandings: %v", err)
	}
	if len(standings.Standings.Results) == 0 {
		t.Fatal("no managers found in league")
	}

	managerID := standings.Standings.Results[0].EntryID
	t.Logf("Fetching history for manager %d (%s)",
		managerID, standings.Standings.Results[0].PlayerName)

	resp, err := client.GetManagerHistory(ctx, managerID)
	if err != nil {
		t.Fatalf("GetManagerHistory: %v", err)
	}

	t.Logf("Gameweeks played: %d, Chips used: %d", len(resp.Current), len(resp.Chips))

	for _, gw := range resp.Current {
		t.Logf("  GW%d: %d pts (total: %d, rank: %d)",
			gw.Event, gw.Points, gw.TotalPoints, gw.OverallRank)
	}
	for _, chip := range resp.Chips {
		t.Logf("  Chip: %s (GW%d)", chip.Name, chip.Event)
	}
}
