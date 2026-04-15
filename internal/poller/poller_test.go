package poller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------
//
// Go pattern — COMPILE-TIME INTERFACE SATISFACTION:
//
// These lines verify at compile time that the concrete types satisfy the
// interfaces the poller depends on. If fpl.Client later removes a method
// the poller needs, this line breaks the build immediately — not at
// runtime during a test or in production.

var _ FPLClient = (*fpl.Client)(nil)

// No compile-time check needed for store.Store — PostgresStore already
// has one in store.go.

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------
//
// Go pattern — HAND-WRITTEN FAKES (not mocks):
//
// Idiomatic Go testing avoids mocking frameworks. Instead, you write
// minimal structs that implement the interface, returning whatever the
// test configures. This is simpler, easier to debug, and doesn't require
// learning a mocking DSL.
//
// The standard library itself uses this pattern extensively (e.g.,
// net/http/httptest.Server is a hand-written fake HTTP server).

// fakeFPLClient implements FPLClient for testing.
type fakeFPLClient struct {
	bootstrap           fpl.BootstrapResponse
	bootstrapErr        error
	eventStatus         fpl.EventStatusResponse
	eventStatusErr      error
	liveByEvent         map[int]fpl.EventLiveResponse
	liveErr             error
	standings           fpl.H2HStandingsResponse
	standingsErr        error
	matches             fpl.H2HMatchesResponse
	matchesByEvent      map[int]fpl.H2HMatchesResponse
	matchesErr          error
	histories           map[int]fpl.ManagerHistoryResponse
	historyErr          error
	picksByManagerEvent map[int]map[int]fpl.ManagerPicksResponse
	picksErr            error
}

func (f *fakeFPLClient) GetBootstrap(_ context.Context) (fpl.BootstrapResponse, error) {
	return f.bootstrap, f.bootstrapErr
}

func (f *fakeFPLClient) GetEventStatus(_ context.Context) (fpl.EventStatusResponse, error) {
	return f.eventStatus, f.eventStatusErr
}

func (f *fakeFPLClient) GetEventLive(_ context.Context, eventID int) (fpl.EventLiveResponse, error) {
	if f.liveErr != nil {
		return fpl.EventLiveResponse{}, f.liveErr
	}
	if resp, ok := f.liveByEvent[eventID]; ok {
		return resp, nil
	}
	return fpl.EventLiveResponse{
		Elements: []fpl.LiveElement{
			{ID: 1, Stats: fpl.LiveElementStats{TotalPoints: intPtr(10)}},
			{ID: 2, Stats: fpl.LiveElementStats{TotalPoints: intPtr(2)}},
		},
	}, nil
}

func (f *fakeFPLClient) GetAllH2HStandings(_ context.Context, _ int) (fpl.H2HStandingsResponse, error) {
	return f.standings, f.standingsErr
}

func (f *fakeFPLClient) GetAllH2HMatches(_ context.Context, _ int, eventID int) (fpl.H2HMatchesResponse, error) {
	if f.matchesErr != nil {
		return fpl.H2HMatchesResponse{}, f.matchesErr
	}
	if resp, ok := f.matchesByEvent[eventID]; ok {
		return resp, nil
	}
	if len(f.matches.Results) > 0 {
		return f.matches, nil
	}
	if len(f.standings.Standings.Results) >= 2 {
		return fpl.H2HMatchesResponse{
			Results: []fpl.H2HMatch{
				{
					ID:           eventID,
					League:       f.standings.League.ID,
					Event:        eventID,
					Entry1Entry:  f.standings.Standings.Results[0].EntryID,
					Entry1Points: 55,
					Entry2Entry:  f.standings.Standings.Results[1].EntryID,
					Entry2Points: 48,
				},
			},
		}, nil
	}
	return fpl.H2HMatchesResponse{}, nil
}

func (f *fakeFPLClient) GetManagerHistory(_ context.Context, managerID int) (fpl.ManagerHistoryResponse, error) {
	if f.historyErr != nil {
		return fpl.ManagerHistoryResponse{}, f.historyErr
	}
	if resp, ok := f.histories[managerID]; ok {
		if len(resp.Current) == 0 {
			resp.Current = defaultCurrentHistories(f.bootstrap.Events)
		}
		return resp, nil
	}
	return fpl.ManagerHistoryResponse{Current: defaultCurrentHistories(f.bootstrap.Events)}, nil
}

func (f *fakeFPLClient) GetManagerPicks(_ context.Context, managerID int, eventID int) (fpl.ManagerPicksResponse, error) {
	if f.picksErr != nil {
		return fpl.ManagerPicksResponse{}, f.picksErr
	}
	if byEvent, ok := f.picksByManagerEvent[managerID]; ok {
		if resp, ok := byEvent[eventID]; ok {
			return resp, nil
		}
	}
	return fpl.ManagerPicksResponse{
		Picks: []fpl.Pick{
			{Element: 1, Position: 1, Multiplier: 2, IsCaptain: true},
			{Element: 2, Position: 2, Multiplier: 1, IsViceCaptain: true},
		},
	}, nil
}

// fakeStore implements store.Store for testing.
//
// Only the methods the poller actually calls need real implementations.
// The rest panic("not implemented") — if the test triggers one of those
// methods, the panic tells you immediately that the poller is calling
// something unexpected.
type fakeStore struct {
	latestEventID    int
	latestEventIDErr error

	upsertedLeagues  []store.League
	upsertedManagers []store.Manager
	snapshotCalls    int
	lastStandings    []store.GameweekStanding
	lastChips        []store.ChipUsage
	lastResults      []store.H2HResult
	lastManagerStats []store.GameweekManagerStat
	snapshotErr      error

	// Backfill-related fields
	storedEventIDs            []int
	storedManagerStatEventIDs []int
	storedAwardEventIDs       []int
	storedEventIDErr          error
	savedEventIDs             []int
	snapshotMetas             []store.SnapshotMeta
}

func (f *fakeStore) GetLatestEventID(_ context.Context, _ int64) (int, error) {
	return f.latestEventID, f.latestEventIDErr
}

func (f *fakeStore) UpsertLeague(_ context.Context, league store.League) error {
	f.upsertedLeagues = append(f.upsertedLeagues, league)
	return nil
}

func (f *fakeStore) UpsertManager(_ context.Context, manager store.Manager) error {
	f.upsertedManagers = append(f.upsertedManagers, manager)
	return nil
}

func (f *fakeStore) SaveGameweekSnapshot(_ context.Context, snap store.GameweekSnapshot) error {
	f.snapshotCalls++
	f.lastStandings = snap.Standings
	f.lastChips = snap.Chips
	f.lastResults = snap.Results
	f.lastManagerStats = snap.ManagerStats
	f.snapshotMetas = append(f.snapshotMetas, snap.Meta)
	if snap.Meta.EventID != 0 {
		f.savedEventIDs = append(f.savedEventIDs, snap.Meta.EventID)
	}
	return f.snapshotErr
}

func (f *fakeStore) GetStoredEventIDs(_ context.Context, _ int64) ([]int, error) {
	return f.storedEventIDs, f.storedEventIDErr
}

func (f *fakeStore) GetStoredManagerStatEventIDs(_ context.Context, _ int64) ([]int, error) {
	if f.storedManagerStatEventIDs == nil {
		return f.storedEventIDs, f.storedEventIDErr
	}
	return f.storedManagerStatEventIDs, f.storedEventIDErr
}

func (f *fakeStore) GetStoredAwardEventIDs(_ context.Context, _ int64) ([]int, error) {
	if f.storedAwardEventIDs == nil {
		return f.storedEventIDs, f.storedEventIDErr
	}
	return f.storedAwardEventIDs, f.storedEventIDErr
}

func (f *fakeStore) UpsertSnapshotMeta(_ context.Context, meta store.SnapshotMeta) error {
	f.snapshotMetas = append(f.snapshotMetas, meta)
	return nil
}

func (f *fakeStore) GetSnapshotMeta(_ context.Context, _ int64, eventID int) (store.SnapshotMeta, error) {
	for _, meta := range f.snapshotMetas {
		if meta.EventID == eventID {
			return meta, nil
		}
	}
	return store.SnapshotMeta{}, store.ErrNotFound
}

// Methods the poller doesn't call — panic to catch unexpected usage.
func (f *fakeStore) UpsertGameweekStanding(context.Context, store.GameweekStanding) error {
	panic("not implemented")
}
func (f *fakeStore) UpsertChipUsage(context.Context, store.ChipUsage) error {
	panic("not implemented")
}
func (f *fakeStore) UpsertH2HResult(context.Context, store.H2HResult) error {
	panic("not implemented")
}
func (f *fakeStore) UpsertGameweekManagerStat(context.Context, store.GameweekManagerStat) error {
	panic("not implemented")
}
func (f *fakeStore) UpsertGameweekAward(context.Context, store.GameweekAward) error {
	panic("not implemented")
}
func (f *fakeStore) SaveGameweekAwards(context.Context, int64, int, []store.GameweekAward) error {
	panic("not implemented")
}
func (f *fakeStore) GetStandings(context.Context, int64, int) ([]store.GameweekStanding, error) {
	panic("not implemented")
}
func (f *fakeStore) GetChipUsage(context.Context, int64, int) ([]store.ChipUsage, error) {
	panic("not implemented")
}
func (f *fakeStore) GetH2HResults(context.Context, int64, int) ([]store.H2HResult, error) {
	panic("not implemented")
}
func (f *fakeStore) GetH2HResultsRange(context.Context, int64, int, int) ([]store.H2HResult, error) {
	panic("not implemented")
}
func (f *fakeStore) GetGameweekManagerStats(context.Context, int64, int) ([]store.GameweekManagerStat, error) {
	panic("not implemented")
}
func (f *fakeStore) GetGameweekAwards(context.Context, int64, int) ([]store.GameweekAward, error) {
	panic("not implemented")
}
func (f *fakeStore) GetManagers(context.Context, int64) ([]store.Manager, error) {
	panic("not implemented")
}
func (f *fakeStore) GetLeague(context.Context, int64) (store.League, error) {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// pastDeadline returns an ISO 8601 timestamp 1 hour in the past.
func pastDeadline() string {
	return time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
}

// futureDeadline returns an ISO 8601 timestamp 1 hour in the future.
func futureDeadline() string {
	return time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
}

// defaultConfig returns a valid Config for testing.
func defaultConfig() Config {
	return Config{
		LeagueID:           42,
		LeagueType:         "h2h",
		IdleInterval:       1 * time.Second,
		LiveInterval:       1 * time.Second,
		ProcessingInterval: 1 * time.Second,
	}
}

// newTestPoller creates a Poller wired with the given fakes.
// It sets bootstrap to non-nil so tick() doesn't always re-fetch during idle.
// It also zeroes out the rate limit delay so tests don't wait on wall-clock time.
func newTestPoller(fc *fakeFPLClient, fs *fakeStore, onFinalized OnGameweekFinalized) *Poller {
	p, _ := New(fc, fs, defaultConfig(), onFinalized, nil)
	// Pre-set bootstrap so we don't re-fetch on every idle tick in tests.
	p.bootstrap = &fc.bootstrap
	// Zero out rate limit so backfill tests run instantly.
	p.rateLimitDelay = 0
	return p
}

func defaultCurrentHistories(events []fpl.Event) []fpl.GameweekHistory {
	var current []fpl.GameweekHistory
	for _, event := range events {
		if !event.Finished {
			continue
		}
		current = append(current, fpl.GameweekHistory{
			Event:         event.ID,
			PointsOnBench: 0,
		})
	}
	return current
}

func intPtr(v int) *int {
	return &v
}

// ---------------------------------------------------------------------------
// Config validation tests
// ---------------------------------------------------------------------------

func TestNew_RejectsClassicLeague(t *testing.T) {
	cfg := defaultConfig()
	cfg.LeagueType = "classic"

	_, err := New(&fakeFPLClient{}, &fakeStore{}, cfg, nil, nil)
	if err == nil {
		t.Fatal("expected error for classic league type, got nil")
	}
}

func TestNew_RejectsZeroIntervals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero idle interval", func(c *Config) { c.IdleInterval = 0 }},
		{"negative live interval", func(c *Config) { c.LiveInterval = -1 * time.Second }},
		{"zero processing interval", func(c *Config) { c.ProcessingInterval = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			tt.mutate(&cfg)
			_, err := New(&fakeFPLClient{}, &fakeStore{}, cfg, nil, nil)
			if err == nil {
				t.Fatal("expected error for invalid interval, got nil")
			}
		})
	}
}

func TestNew_AcceptsH2H(t *testing.T) {
	p, err := New(&fakeFPLClient{}, &fakeStore{}, defaultConfig(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil poller")
	}
}

// ---------------------------------------------------------------------------
// State transition tests (table-driven)
// ---------------------------------------------------------------------------
//
// Go pattern — TABLE-DRIVEN TESTS:
//
// Each test case configures the fakes and asserts the resulting state after
// one tick. This is the idiomatic Go testing pattern — a slice of test
// cases, each with a descriptive name, run in a loop with t.Run().
//
// Benefits over individual test functions:
//   - Adding a new case is one struct literal, not a new function
//   - All related tests are co-located and easy to compare
//   - The test runner shows "TestTick/name" for each case

func TestTick(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(fc *fakeFPLClient, fs *fakeStore, p *Poller)
		onFinalized   func(context.Context, int) error // if non-nil, used as the callback
		wantState     State
		wantFinalized bool // whether finalization should have occurred
		wantErrSubstr string
	}{
		{
			name: "idle when no current event",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 1, Finished: true, IsPrevious: true, DeadlineTime: pastDeadline()},
					},
				}
			},
			wantState: StateIdle,
		},
		{
			name: "idle when deadline not passed",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, DeadlineTime: futureDeadline()},
					},
				}
			},
			wantState: StateIdle,
		},
		{
			name: "idle when event already processed",
			setup: func(fc *fakeFPLClient, _ *fakeStore, p *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 3, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				p.lastProcessedEvent = 3
			},
			wantState: StateIdle,
		},
		{
			name: "live when deadline passed but not finished",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: false, DeadlineTime: pastDeadline()},
					},
				}
			},
			wantState: StateLive,
		},
		{
			name: "processing when finished but bonus not added",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status: []fpl.EventStatus{
						{Event: 5, BonusAdded: false},
					},
					Leagues: "Updated",
				}
			},
			wantState: StateProcessing,
		},
		{
			name: "processing when finished but leagues not updated",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status: []fpl.EventStatus{
						{Event: 5, BonusAdded: true},
					},
					Leagues: "Updating",
				}
			},
			wantState: StateProcessing,
		},
		{
			name: "finalization triggers on fully finalized event",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status: []fpl.EventStatus{
						{Event: 5, BonusAdded: true},
					},
					Leagues: "Updated",
				}
				fc.standings = fpl.H2HStandingsResponse{
					League: fpl.LeagueInfo{ID: 42, Name: "Test League"},
					Standings: fpl.Standings{
						Results: []fpl.StandingEntry{
							{EntryID: 100, PlayerName: "Alice", EntryName: "Team A", Rank: 1, Total: 9, PointsFor: 200},
						},
					},
				}
				fc.histories = map[int]fpl.ManagerHistoryResponse{
					100: {Chips: []fpl.ChipUsage{{Event: 5, Name: "wildcard"}}},
				}
				fc.matches = fpl.H2HMatchesResponse{
					Results: []fpl.H2HMatch{
						{ID: 1, League: 42, Event: 5, Entry1Entry: 100, Entry1Points: 72, Entry2Entry: 200, Entry2Points: 61},
					},
				}
			},
			wantState:     StateIdle,
			wantFinalized: true,
		},
		{
			name: "finalization with multiple matchdays all bonus added",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status: []fpl.EventStatus{
						{Event: 5, BonusAdded: true, Date: "2025-01-01"},
						{Event: 5, BonusAdded: true, Date: "2025-01-02"},
						{Event: 5, BonusAdded: true, Date: "2025-01-03"},
					},
					Leagues: "Updated",
				}
				fc.standings = fpl.H2HStandingsResponse{
					League:    fpl.LeagueInfo{ID: 42, Name: "Test League"},
					Standings: fpl.Standings{Results: []fpl.StandingEntry{}},
				}
				fc.matches = fpl.H2HMatchesResponse{Results: []fpl.H2HMatch{}}
			},
			wantState:     StateIdle,
			wantFinalized: true,
		},
		{
			name: "processing when one matchday bonus not added",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status: []fpl.EventStatus{
						{Event: 5, BonusAdded: true, Date: "2025-01-01"},
						{Event: 5, BonusAdded: false, Date: "2025-01-02"},
					},
					Leagues: "Updated",
				}
			},
			wantState: StateProcessing,
		},
		{
			name: "error propagated when bootstrap fails",
			setup: func(fc *fakeFPLClient, _ *fakeStore, p *Poller) {
				fc.bootstrapErr = errors.New("network timeout")
				// Force bootstrap refresh by clearing it.
				p.bootstrap = nil
			},
			wantState:     StateIdle, // state unchanged from initial
			wantErrSubstr: "bootstrap",
		},
		{
			name: "error propagated when event status fails",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatusErr = errors.New("503 service unavailable")
			},
			wantState:     StateIdle,
			wantErrSubstr: "event status",
		},
		{
			name: "game updating during event status keeps state processing without error",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatusErr = fpl.ErrGameUpdating
			},
			wantState: StateProcessing,
		},
		{
			name: "finalization failure keeps state processing and does not advance guard",
			setup: func(fc *fakeFPLClient, fs *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status:  []fpl.EventStatus{{Event: 5, BonusAdded: true}},
					Leagues: "Updated",
				}
				fc.standingsErr = errors.New("connection refused")
			},
			wantState:     StateProcessing,
			wantErrSubstr: "finalization",
		},
		{
			// Callback failure: snapshot IS saved (step 6), but the callback
			// (step 7) fails. The guard is NOT advanced, so the poller retries.
			// wantFinalized is true because the snapshot was saved — the test
			// for guard non-advancement is in TestTick_CallbackFailureDoesNotAdvanceGuard.
			name: "callback failure aborts finalization",
			onFinalized: func(_ context.Context, _ int) error {
				return errors.New("stats engine crashed")
			},
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
					},
				}
				fc.eventStatus = fpl.EventStatusResponse{
					Status:  []fpl.EventStatus{{Event: 5, BonusAdded: true}},
					Leagues: "Updated",
				}
				fc.standings = fpl.H2HStandingsResponse{
					League:    fpl.LeagueInfo{ID: 42, Name: "Test League"},
					Standings: fpl.Standings{Results: []fpl.StandingEntry{}},
				}
				fc.matches = fpl.H2HMatchesResponse{Results: []fpl.H2HMatch{}}
			},
			wantState:     StateProcessing,
			wantFinalized: true, // snapshot saved before callback failed
			wantErrSubstr: "finalization",
		},
		{
			name: "next event with passed deadline treated as relevant",
			setup: func(fc *fakeFPLClient, _ *fakeStore, _ *Poller) {
				fc.bootstrap = fpl.BootstrapResponse{
					Events: []fpl.Event{
						{ID: 4, IsPrevious: true, Finished: true, DeadlineTime: pastDeadline()},
						{ID: 5, IsNext: true, Finished: false, DeadlineTime: pastDeadline()},
					},
				}
			},
			wantState: StateLive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := &fakeFPLClient{
				histories: make(map[int]fpl.ManagerHistoryResponse),
			}
			fs := &fakeStore{}

			p := newTestPoller(fc, fs, tt.onFinalized)
			tt.setup(fc, fs, p)

			// Re-set bootstrap on the poller after setup modifies fc.bootstrap,
			// unless setup already cleared it (for bootstrap error tests).
			if p.bootstrap != nil {
				p.bootstrap = &fc.bootstrap
			}

			err := p.tick(context.Background())

			// Check error expectation.
			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErrSubstr, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check state.
			if p.state != tt.wantState {
				t.Errorf("state = %q, want %q", p.state, tt.wantState)
			}

			// Check finalization occurred.
			if tt.wantFinalized && fs.snapshotCalls == 0 {
				t.Error("expected finalization (snapshot save), but it didn't happen")
			}
			if !tt.wantFinalized && fs.snapshotCalls > 0 {
				t.Error("did not expect finalization, but snapshot was saved")
			}
		})
	}
}

// TestTick_FinalizationAdvancesGuard verifies that after successful
// finalization, lastProcessedEvent is updated and a second tick for
// the same event stays idle.
func TestTick_FinalizationAdvancesGuard(t *testing.T) {
	fc := &fakeFPLClient{
		bootstrap: fpl.BootstrapResponse{
			Events: []fpl.Event{
				{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
			},
		},
		eventStatus: fpl.EventStatusResponse{
			Status:  []fpl.EventStatus{{Event: 5, BonusAdded: true}},
			Leagues: "Updated",
		},
		standings: fpl.H2HStandingsResponse{
			League:    fpl.LeagueInfo{ID: 42, Name: "Test League"},
			Standings: fpl.Standings{Results: []fpl.StandingEntry{}},
		},
		matches:   fpl.H2HMatchesResponse{Results: []fpl.H2HMatch{}},
		histories: make(map[int]fpl.ManagerHistoryResponse),
	}
	fs := &fakeStore{}

	p := newTestPoller(fc, fs, nil)

	// First tick: should finalize.
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if p.lastProcessedEvent != 5 {
		t.Fatalf("lastProcessedEvent = %d, want 5", p.lastProcessedEvent)
	}

	// Restore bootstrap (finalization clears it to trigger refresh).
	p.bootstrap = &fc.bootstrap

	// Second tick: same event → should stay idle, no re-finalization.
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if p.state != StateIdle {
		t.Errorf("state = %q after second tick, want idle", p.state)
	}
	if fs.snapshotCalls != 1 {
		t.Errorf("snapshot called %d times, want 1 (no re-processing)", fs.snapshotCalls)
	}
}

// TestTick_LiveStateRefreshesBootstrap is a regression test for a bug where
// the poller would stay stuck in StateLive forever. The root cause: bootstrap
// was only refreshed during StateIdle, but event.Finished (the field that
// drives the StateLive → StateProcessing transition) lives in the bootstrap
// response. Once the poller entered StateLive, it read stale data every tick.
//
// This test verifies that a bootstrap update (Finished changing from false to
// true) is observed during StateLive, causing the correct state transition.
func TestTick_LiveStateRefreshesBootstrap(t *testing.T) {
	fc := &fakeFPLClient{
		bootstrap: fpl.BootstrapResponse{
			Events: []fpl.Event{
				{ID: 32, IsCurrent: true, Finished: false, DeadlineTime: pastDeadline()},
			},
		},
		// BonusAdded: false keeps the poller in StateProcessing rather than
		// triggering full finalization (which would require standings/histories/picks).
		eventStatus: fpl.EventStatusResponse{
			Status:  []fpl.EventStatus{{Event: 32, BonusAdded: false}},
			Leagues: "Updating",
		},
	}
	fs := &fakeStore{}

	p := newTestPoller(fc, fs, nil)

	// Tick 1: bootstrap returns Finished: false → should stay StateLive.
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if p.state != StateLive {
		t.Fatalf("state after tick 1 = %q, want %q", p.state, StateLive)
	}

	// Simulate the FPL API updating: fixtures are now done.
	fc.bootstrap.Events[0].Finished = true

	// Tick 2: bootstrap should be re-fetched, Finished: true is seen,
	// and the poller should advance to StateProcessing.
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if p.state != StateProcessing {
		t.Errorf("state after tick 2 = %q, want %q (bootstrap was not refreshed in StateLive)", p.state, StateProcessing)
	}
}

// TestTick_FinalizationPersistsCorrectData verifies the data that flows
// through the finalization pipeline: league, managers, standings, chips.
func TestTick_FinalizationPersistsCorrectData(t *testing.T) {
	fc := &fakeFPLClient{
		bootstrap: fpl.BootstrapResponse{
			Events: []fpl.Event{
				{ID: 10, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
			},
		},
		eventStatus: fpl.EventStatusResponse{
			Status:  []fpl.EventStatus{{Event: 10, BonusAdded: true}},
			Leagues: "Updated",
		},
		standings: fpl.H2HStandingsResponse{
			League: fpl.LeagueInfo{ID: 42, Name: "Capital FC"},
			Standings: fpl.Standings{
				Results: []fpl.StandingEntry{
					{EntryID: 100, PlayerName: "Alice", EntryName: "Team A", Rank: 1, Total: 15, PointsFor: 500},
					{EntryID: 200, PlayerName: "Bob", EntryName: "Team B", Rank: 2, Total: 12, PointsFor: 480},
				},
			},
		},
		matches: fpl.H2HMatchesResponse{
			Results: []fpl.H2HMatch{
				{ID: 1, League: 42, Event: 10, Entry1Entry: 200, Entry1Points: 48, Entry2Entry: 100, Entry2Points: 65},
			},
		},
		histories: map[int]fpl.ManagerHistoryResponse{
			100: {Chips: []fpl.ChipUsage{
				{Event: 10, Name: "wildcard"},
				{Event: 5, Name: "bboost"}, // old chip — should be filtered out
			}},
			200: {Chips: []fpl.ChipUsage{}},
		},
	}
	fs := &fakeStore{}
	p := newTestPoller(fc, fs, nil)

	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Verify league was upserted.
	if len(fs.upsertedLeagues) != 1 {
		t.Fatalf("upserted %d leagues, want 1", len(fs.upsertedLeagues))
	}
	if fs.upsertedLeagues[0].Name != "Capital FC" {
		t.Errorf("league name = %q, want %q", fs.upsertedLeagues[0].Name, "Capital FC")
	}
	if fs.upsertedLeagues[0].Type != "h2h" {
		t.Errorf("league type = %q, want %q", fs.upsertedLeagues[0].Type, "h2h")
	}

	// Verify managers were upserted.
	if len(fs.upsertedManagers) != 2 {
		t.Fatalf("upserted %d managers, want 2", len(fs.upsertedManagers))
	}

	// Verify standings snapshot.
	if len(fs.lastStandings) != 2 {
		t.Fatalf("saved %d standings, want 2", len(fs.lastStandings))
	}
	if fs.lastStandings[0].EventID != 10 {
		t.Errorf("standing event_id = %d, want 10", fs.lastStandings[0].EventID)
	}
	if fs.lastStandings[0].Points != 15 {
		t.Errorf("standing points = %d, want 15", fs.lastStandings[0].Points)
	}
	if fs.lastStandings[0].TotalScore != 500 {
		t.Errorf("standing total_score = %d, want 500", fs.lastStandings[0].TotalScore)
	}

	// Verify chips — only GW 10 chip, not the old GW 5 chip.
	if len(fs.lastChips) != 1 {
		t.Fatalf("saved %d chips, want 1 (only GW 10)", len(fs.lastChips))
	}
	if fs.lastChips[0].Chip != "wildcard" {
		t.Errorf("chip = %q, want %q", fs.lastChips[0].Chip, "wildcard")
	}
	if fs.lastChips[0].ManagerID != 100 {
		t.Errorf("chip manager_id = %d, want 100", fs.lastChips[0].ManagerID)
	}

	// Verify H2H results were canonicalized before persistence.
	if len(fs.lastResults) != 1 {
		t.Fatalf("saved %d h2h results, want 1", len(fs.lastResults))
	}
	if fs.lastResults[0].Manager1ID != 100 || fs.lastResults[0].Manager2ID != 200 {
		t.Errorf("result manager order = %d/%d, want 100/200", fs.lastResults[0].Manager1ID, fs.lastResults[0].Manager2ID)
	}
	if fs.lastResults[0].Manager1Score != 65 || fs.lastResults[0].Manager2Score != 48 {
		t.Errorf("result scores = %d/%d, want 65/48", fs.lastResults[0].Manager1Score, fs.lastResults[0].Manager2Score)
	}
}

// TestTick_CallbackFailureDoesNotAdvanceGuard verifies that if the
// onFinalized callback returns an error, lastProcessedEvent is NOT
// updated — ensuring the poller retries on the next tick.
func TestTick_CallbackFailureDoesNotAdvanceGuard(t *testing.T) {
	fc := &fakeFPLClient{
		bootstrap: fpl.BootstrapResponse{
			Events: []fpl.Event{
				{ID: 5, IsCurrent: true, Finished: true, DeadlineTime: pastDeadline()},
			},
		},
		eventStatus: fpl.EventStatusResponse{
			Status:  []fpl.EventStatus{{Event: 5, BonusAdded: true}},
			Leagues: "Updated",
		},
		standings: fpl.H2HStandingsResponse{
			League:    fpl.LeagueInfo{ID: 42, Name: "Test League"},
			Standings: fpl.Standings{Results: []fpl.StandingEntry{}},
		},
		matches:   fpl.H2HMatchesResponse{Results: []fpl.H2HMatch{}},
		histories: make(map[int]fpl.ManagerHistoryResponse),
	}
	fs := &fakeStore{}

	callbackCalled := false
	failingCallback := func(_ context.Context, _ int) error {
		callbackCalled = true
		return errors.New("stats engine crashed")
	}

	p := newTestPoller(fc, fs, failingCallback)

	err := p.tick(context.Background())
	if err == nil {
		t.Fatal("expected error from callback failure, got nil")
	}

	if !callbackCalled {
		t.Error("expected callback to be called")
	}

	// The snapshot WAS saved (step 6 succeeded), but the guard was NOT
	// advanced because the callback (step 7) failed.
	if fs.snapshotCalls != 1 {
		t.Errorf("snapshot calls = %d, want 1", fs.snapshotCalls)
	}
	if p.lastProcessedEvent != 0 {
		t.Errorf("lastProcessedEvent = %d, want 0 (not advanced)", p.lastProcessedEvent)
	}
	if p.state != StateProcessing {
		t.Errorf("state = %q, want %q", p.state, StateProcessing)
	}
}

// ---------------------------------------------------------------------------
// allBonusAdded tests
// ---------------------------------------------------------------------------

func TestAllBonusAdded(t *testing.T) {
	tests := []struct {
		name    string
		resp    fpl.EventStatusResponse
		eventID int
		want    bool
	}{
		{
			name:    "no status entries for event",
			resp:    fpl.EventStatusResponse{Status: []fpl.EventStatus{}},
			eventID: 5,
			want:    false,
		},
		{
			name: "single entry all bonus added",
			resp: fpl.EventStatusResponse{
				Status: []fpl.EventStatus{{Event: 5, BonusAdded: true}},
			},
			eventID: 5,
			want:    true,
		},
		{
			name: "single entry bonus not added",
			resp: fpl.EventStatusResponse{
				Status: []fpl.EventStatus{{Event: 5, BonusAdded: false}},
			},
			eventID: 5,
			want:    false,
		},
		{
			name: "multiple entries all added",
			resp: fpl.EventStatusResponse{
				Status: []fpl.EventStatus{
					{Event: 5, BonusAdded: true},
					{Event: 5, BonusAdded: true},
					{Event: 5, BonusAdded: true},
				},
			},
			eventID: 5,
			want:    true,
		},
		{
			name: "multiple entries one not added",
			resp: fpl.EventStatusResponse{
				Status: []fpl.EventStatus{
					{Event: 5, BonusAdded: true},
					{Event: 5, BonusAdded: false},
					{Event: 5, BonusAdded: true},
				},
			},
			eventID: 5,
			want:    false,
		},
		{
			name: "entries for different event ignored",
			resp: fpl.EventStatusResponse{
				Status: []fpl.EventStatus{
					{Event: 4, BonusAdded: true},
					{Event: 5, BonusAdded: true},
				},
			},
			eventID: 5,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allBonusAdded(tt.resp, tt.eventID)
			if got != tt.want {
				t.Errorf("allBonusAdded() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// intervalForState tests
// ---------------------------------------------------------------------------

func TestIntervalForState(t *testing.T) {
	cfg := Config{
		LeagueID:           1,
		LeagueType:         "h2h",
		IdleInterval:       6 * time.Hour,
		LiveInterval:       15 * time.Minute,
		ProcessingInterval: 10 * time.Minute,
	}
	p, _ := New(&fakeFPLClient{}, &fakeStore{}, cfg, nil, nil)

	tests := []struct {
		state State
		want  time.Duration
	}{
		{StateIdle, 6 * time.Hour},
		{StateLive, 15 * time.Minute},
		{StateProcessing, 10 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			p.state = tt.state
			got := p.intervalForState()
			if got != tt.want {
				t.Errorf("intervalForState() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Mapper tests
// ---------------------------------------------------------------------------
//
// Pure function tests — no fakes needed. Input → output.

func TestMapLeague(t *testing.T) {
	info := fpl.LeagueInfo{ID: 916670, Name: "Capital FC", Scoring: "h"}
	got := mapLeague(info, "h2h")

	if got.ID != 916670 {
		t.Errorf("ID = %d, want 916670", got.ID)
	}
	if got.Name != "Capital FC" {
		t.Errorf("Name = %q, want %q", got.Name, "Capital FC")
	}
	if got.Type != "h2h" {
		t.Errorf("Type = %q, want %q", got.Type, "h2h")
	}
}

func TestMapManager(t *testing.T) {
	entry := fpl.StandingEntry{
		EntryID:    12345,
		PlayerName: "Chris Longe",
		EntryName:  "The Banter Bus",
	}
	got := mapManager(916670, entry)

	if got.LeagueID != 916670 {
		t.Errorf("LeagueID = %d, want 916670", got.LeagueID)
	}
	if got.ID != 12345 {
		t.Errorf("ID = %d, want 12345", got.ID)
	}
	if got.Name != "Chris Longe" {
		t.Errorf("Name = %q, want %q", got.Name, "Chris Longe")
	}
	if got.TeamName != "The Banter Bus" {
		t.Errorf("TeamName = %q, want %q", got.TeamName, "The Banter Bus")
	}
}

func TestMapStandings(t *testing.T) {
	entries := []fpl.StandingEntry{
		{EntryID: 100, Rank: 1, Total: 15, PointsFor: 500},
		{EntryID: 200, Rank: 2, Total: 12, PointsFor: 480},
	}
	got := mapStandings(42, 10, entries)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	// First entry.
	if got[0].LeagueID != 42 {
		t.Errorf("[0].LeagueID = %d, want 42", got[0].LeagueID)
	}
	if got[0].EventID != 10 {
		t.Errorf("[0].EventID = %d, want 10", got[0].EventID)
	}
	if got[0].ManagerID != 100 {
		t.Errorf("[0].ManagerID = %d, want 100", got[0].ManagerID)
	}
	if got[0].Points != 15 {
		t.Errorf("[0].Points = %d, want 15 (H2H points)", got[0].Points)
	}
	if got[0].TotalScore != 500 {
		t.Errorf("[0].TotalScore = %d, want 500 (PointsFor)", got[0].TotalScore)
	}
}

func TestMapStandings_EmptyInput(t *testing.T) {
	got := mapStandings(42, 10, []fpl.StandingEntry{})
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 for empty input", len(got))
	}
}

func TestMapChipUsages(t *testing.T) {
	chips := []fpl.ChipUsage{
		{Event: 10, Name: "wildcard"},
		{Event: 5, Name: "bboost"},   // different GW — should be filtered
		{Event: 10, Name: "freehit"}, // same GW — should be included
	}
	got := mapChipUsages(42, 10, 100, chips)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (only GW 10 chips)", len(got))
	}
	if got[0].Chip != "wildcard" {
		t.Errorf("[0].Chip = %q, want %q", got[0].Chip, "wildcard")
	}
	if got[1].Chip != "freehit" {
		t.Errorf("[1].Chip = %q, want %q", got[1].Chip, "freehit")
	}
	if got[0].LeagueID != 42 || got[0].ManagerID != 100 || got[0].EventID != 10 {
		t.Errorf("unexpected IDs: league=%d manager=%d event=%d", got[0].LeagueID, got[0].ManagerID, got[0].EventID)
	}
}

func TestMapChipUsages_NoChipsThisGW(t *testing.T) {
	chips := []fpl.ChipUsage{
		{Event: 3, Name: "bboost"},
		{Event: 7, Name: "wildcard"},
	}
	got := mapChipUsages(42, 10, 100, chips)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (no chips for GW 10)", len(got))
	}
}

func TestMapChipUsages_EmptyInput(t *testing.T) {
	got := mapChipUsages(42, 10, 100, []fpl.ChipUsage{})
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 for empty input", len(got))
	}
}

func TestMapChipUsages_NilInput(t *testing.T) {
	got := mapChipUsages(42, 10, 100, nil)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 for nil input", len(got))
	}
}
