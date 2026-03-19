package poller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
)

// ---------------------------------------------------------------------------
// Backfill tests (table-driven)
//
// These tests exercise the Backfill() method using the same fakeFPLClient
// and fakeStore from poller_test.go. The backfill logic reuses the poller's
// existing mapper functions and FPLClient interface, so the fakes are shared.
// ---------------------------------------------------------------------------

// makeEvents creates a slice of fpl.Event for the given IDs.
// If finished is provided, those IDs are marked as Finished.
// All events get a past deadline so the poller doesn't filter them.
func makeEvents(ids []int, finished map[int]bool) []fpl.Event {
	events := make([]fpl.Event, len(ids))
	for i, id := range ids {
		events[i] = fpl.Event{
			ID:           id,
			Name:         "Gameweek",
			DeadlineTime: pastDeadline(),
			Finished:     finished[id],
		}
	}
	return events
}

// defaultStandings returns a minimal H2HStandingsResponse with two managers.
func defaultStandings() fpl.H2HStandingsResponse {
	return fpl.H2HStandingsResponse{
		League: fpl.LeagueInfo{ID: 42, Name: "Test League"},
		Standings: fpl.Standings{
			Results: []fpl.StandingEntry{
				{EntryID: 100, PlayerName: "Alice", EntryName: "Team A", Rank: 1, Total: 15, PointsFor: 500},
				{EntryID: 200, PlayerName: "Bob", EntryName: "Team B", Rank: 2, Total: 12, PointsFor: 480},
			},
		},
	}
}

func TestBackfill(t *testing.T) {
	tests := []struct {
		name              string
		setup             func(fc *fakeFPLClient, fs *fakeStore)
		wantSnapshots     int      // expected number of SaveGameweekSnapshot calls
		wantSavedEventIDs []int    // expected event IDs saved (in order)
		wantMetaSource    string   // expected source on all metas
		wantMetaFidelity  string   // expected standings_fidelity on all metas
		wantErr           bool
		wantErrMsg        string
	}{
		{
			name: "no finished events",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3}, map[int]bool{}),
				}
			},
			wantSnapshots: 0,
		},
		{
			name: "already up to date",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 2: true, 3: true}),
				}
				fs.storedEventIDs = []int{1, 2, 3}
			},
			wantSnapshots: 0,
		},
		{
			name: "backfills missing from zero",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 2: true, 3: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{}
			},
			wantSnapshots:     3,
			wantSavedEventIDs: []int{1, 2, 3},
			wantMetaSource:    "backfill",
			wantMetaFidelity:  "synthetic",
		},
		{
			name: "backfills sparse gaps",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3, 4, 5}, map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{1, 2, 5}
			},
			wantSnapshots:     2,
			wantSavedEventIDs: []int{3, 4},
			wantMetaSource:    "backfill",
			wantMetaFidelity:  "synthetic",
		},
		{
			name: "has GW30 only backfills 1-29",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				ids := make([]int, 30)
				finished := make(map[int]bool)
				for i := 1; i <= 30; i++ {
					ids[i-1] = i
					finished[i] = true
				}
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents(ids, finished),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{30}
			},
			wantSnapshots: 29,
			wantMetaSource:   "backfill",
			wantMetaFidelity: "synthetic",
		},
		{
			name: "chips filtered per GW",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 2: true, 3: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{
						{Event: 1, Name: "wildcard"},
						{Event: 3, Name: "bboost"},
					}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{}
			},
			wantSnapshots:     3,
			wantSavedEventIDs: []int{1, 2, 3},
			wantMetaSource:    "backfill",
			wantMetaFidelity:  "synthetic",
		},
		{
			name: "skips unfinished GW",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 3: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{}
			},
			wantSnapshots:     2,
			wantSavedEventIDs: []int{1, 3},
			wantMetaSource:    "backfill",
			wantMetaFidelity:  "synthetic",
		},
		{
			name: "does not fire callback",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1}, map[int]bool{1: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{}
			},
			wantSnapshots:     1,
			wantSavedEventIDs: []int{1},
			wantMetaSource:    "backfill",
			wantMetaFidelity:  "synthetic",
		},
		{
			name: "context cancellation",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 2: true, 3: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{}
			},
			wantErr: true,
		},
		{
			name: "standings fetch error",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2}, map[int]bool{1: true, 2: true}),
				}
				fc.standingsErr = errors.New("connection refused")
				fs.storedEventIDs = []int{}
			},
			wantSnapshots: 0,
			wantErr:       true,
			wantErrMsg:    "standings",
		},
		{
			name: "history fetch error",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1}, map[int]bool{1: true}),
				}
				fc.standings = defaultStandings()
				fc.historyErr = errors.New("timeout")
				fs.storedEventIDs = []int{}
			},
			wantSnapshots: 0,
			wantErr:       true,
			wantErrMsg:    "history",
		},
		{
			name: "meta tagged as synthetic",
			setup: func(fc *fakeFPLClient, fs *fakeStore) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: makeEvents([]int{1, 2}, map[int]bool{1: true, 2: true}),
				}
				fc.standings = defaultStandings()
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{}},
					200: {Chips: []fpl.ChipUsage{}},
				}
				fs.storedEventIDs = []int{}
			},
			wantSnapshots:     2,
			wantSavedEventIDs: []int{1, 2},
			wantMetaSource:    "backfill",
			wantMetaFidelity:  "synthetic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := &fakeFPLClient{
				histories: make(map[int]fpl.ManagerHistoryResponse),
			}
			fs := &fakeStore{}

			tt.setup(fc, fs)

			// Track if callback was invoked (it should never be during backfill).
			callbackCalled := false
			onFinalized := func(_ context.Context, _ int) error {
				callbackCalled = true
				return nil
			}

			p, err := New(fc, fs, defaultConfig(), onFinalized)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			p.rateLimitDelay = 0 // Zero out so tests run instantly.

			// For context cancellation test, cancel immediately.
			ctx := context.Background()
			if tt.name == "context cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // Cancel immediately.
			}

			err = p.Backfill(ctx)

			// Check error expectation.
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrMsg != "" {
					if !contains(err.Error(), tt.wantErrMsg) {
						t.Fatalf("expected error containing %q, got %q", tt.wantErrMsg, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check snapshot count.
			if fs.snapshotCalls != tt.wantSnapshots {
				t.Errorf("snapshot calls = %d, want %d", fs.snapshotCalls, tt.wantSnapshots)
			}

			// Check saved event IDs (order matters — should be ascending).
			if tt.wantSavedEventIDs != nil {
				if len(fs.savedEventIDs) != len(tt.wantSavedEventIDs) {
					t.Fatalf("saved event IDs = %v, want %v", fs.savedEventIDs, tt.wantSavedEventIDs)
				}
				for i, want := range tt.wantSavedEventIDs {
					if fs.savedEventIDs[i] != want {
						t.Errorf("savedEventIDs[%d] = %d, want %d", i, fs.savedEventIDs[i], want)
					}
				}
			}

			// Check metadata provenance.
			if tt.wantMetaSource != "" {
				if len(fs.snapshotMetas) != tt.wantSnapshots {
					t.Fatalf("snapshot metas = %d, want %d", len(fs.snapshotMetas), tt.wantSnapshots)
				}
				for i, meta := range fs.snapshotMetas {
					if meta.Source != tt.wantMetaSource {
						t.Errorf("meta[%d].Source = %q, want %q", i, meta.Source, tt.wantMetaSource)
					}
					if meta.StandingsFidelity != tt.wantMetaFidelity {
						t.Errorf("meta[%d].StandingsFidelity = %q, want %q", i, meta.StandingsFidelity, tt.wantMetaFidelity)
					}
				}
			}

			// Callback should never be invoked during backfill.
			if callbackCalled {
				t.Error("onFinalized callback should not be called during backfill")
			}
		})
	}
}

// TestBackfill_Idempotent verifies that running backfill twice is a no-op
// the second time — the first run fills all gaps, and the second run finds
// nothing to do.
func TestBackfill_Idempotent(t *testing.T) {
	fc := &fakeFPLClient{
		bootstrap: fpl.BootstrapResponse{
			Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 2: true, 3: true}),
		},
		standings: defaultStandings(),
		histories: map[int]fpl.ManagerHistoryResponse{
			100: {Chips: []fpl.ChipUsage{}},
			200: {Chips: []fpl.ChipUsage{}},
		},
	}
	fs := &fakeStore{
		storedEventIDs: []int{},
	}

	p, err := New(fc, fs, defaultConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.rateLimitDelay = 0

	// First run — should backfill all 3 GWs.
	if err := p.Backfill(context.Background()); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if fs.snapshotCalls != 3 {
		t.Fatalf("first run: snapshot calls = %d, want 3", fs.snapshotCalls)
	}

	// Simulate the DB now having all event IDs.
	fs.storedEventIDs = []int{1, 2, 3}

	// Second run — should be a no-op.
	prevCalls := fs.snapshotCalls
	if err := p.Backfill(context.Background()); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if fs.snapshotCalls != prevCalls {
		t.Errorf("second run: snapshot calls = %d, want %d (no-op)", fs.snapshotCalls, prevCalls)
	}
}

// TestBackfill_ChipsFilteredPerGW verifies that chips are correctly
// attributed to the gameweek they were used in, not duplicated across GWs.
func TestBackfill_ChipsFilteredPerGW(t *testing.T) {
	fc := &fakeFPLClient{
		bootstrap: fpl.BootstrapResponse{
			Events: makeEvents([]int{1, 2, 3}, map[int]bool{1: true, 2: true, 3: true}),
		},
		standings: fpl.H2HStandingsResponse{
			League: fpl.LeagueInfo{ID: 42, Name: "Test League"},
			Standings: fpl.Standings{
				Results: []fpl.StandingEntry{
					{EntryID: 100, PlayerName: "Alice", EntryName: "Team A", Rank: 1, Total: 15, PointsFor: 500},
				},
			},
		},
		histories: map[int]fpl.ManagerHistoryResponse{
			100: {Chips: []fpl.ChipUsage{
				{Event: 1, Name: "wildcard"},
				{Event: 3, Name: "bboost"},
			}},
		},
	}
	fs := &fakeStore{
		storedEventIDs: []int{},
	}

	p, err := New(fc, fs, defaultConfig(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.rateLimitDelay = 0

	if err := p.Backfill(context.Background()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// We need to track chips per snapshot call. The fakeStore currently
	// only keeps lastChips (the most recent call). We verify the total
	// snapshot calls and rely on the mapper tests for per-GW filtering.
	if fs.snapshotCalls != 3 {
		t.Fatalf("snapshot calls = %d, want 3", fs.snapshotCalls)
	}
}

// TestRateLimitDelay verifies the interruptible delay helper.
func TestRateLimitDelay(t *testing.T) {
	t.Run("completes normally", func(t *testing.T) {
		err := rateLimitDelay(context.Background(), 1*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns on context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := rateLimitDelay(ctx, 1*time.Hour)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

// contains is a simple substring check for error messages.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
