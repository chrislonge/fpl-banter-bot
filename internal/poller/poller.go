package poller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// State represents the gameweek lifecycle phase.
//
// Go pattern — NAMED STRING TYPE FOR ENUMS:
//
// Using `type State string` (not iota) gives human-readable log output.
// With iota you'd see "state=2" in logs; with string you see "state=processing".
// This matters for operational debugging — when you're watching logs at 2am
// trying to figure out why the bot isn't posting, "state=processing" is
// immediately understandable.
type State string

const (
	// StateIdle means we're waiting for a gameweek to become relevant.
	// This is the resting state between gameweeks, during off-season,
	// or when the current GW has already been processed.
	StateIdle State = "idle"

	// StateLive means a gameweek's deadline has passed and fixtures are
	// in progress. The bot polls more frequently to detect when fixtures
	// finish, but doesn't collect data yet.
	StateLive State = "live"

	// StateProcessing means all fixtures are finished but the FPL system
	// hasn't finalized bonus points and league tables yet. The bot polls
	// frequently, waiting for finalization signals.
	StateProcessing State = "processing"
)

// Note: there is no StateFinalized constant. Finalization is a one-shot
// action that immediately transitions back to StateIdle — it's not a state
// the poller lingers in. The state machine has only 3 resting states.

// FPLClient defines the FPL API methods the poller needs.
//
// Go pattern — CONSUMER-DEFINED INTERFACE (Interface Segregation Principle):
//
// In Go, interfaces are defined by the package that *uses* them, not the
// package that implements them. You'll see this in the standard library:
// io.Reader is defined by consumers, not by os.File.
//
// The full fpl.Client has 6 methods. The poller only needs 4. By defining
// a narrow interface here, we:
//   - Document exactly what the poller depends on
//   - Make it easy to write test fakes (4 methods, not 6)
//   - Prevent accidental coupling to methods we don't need
//
// The concrete *fpl.Client satisfies this interface implicitly — no cast
// or "implements" keyword needed. This is Go's structural typing.
type FPLClient interface {
	GetBootstrap(ctx context.Context) (fpl.BootstrapResponse, error)
	GetEventStatus(ctx context.Context) (fpl.EventStatusResponse, error)
	GetAllH2HStandings(ctx context.Context, leagueID int) (fpl.H2HStandingsResponse, error)
	GetManagerHistory(ctx context.Context, managerID int) (fpl.ManagerHistoryResponse, error)
}

// OnGameweekFinalized is called after a gameweek's data is persisted.
//
// Go pattern — FUNCTION TYPE AS SINGLE-METHOD INTERFACE:
//
// When you only need one method, a function type is more idiomatic than
// a full interface in Go. This is the Hollywood Principle — "don't call
// us, we'll call you." The stats engine will provide this callback in
// Phase 1.5. Until then, main.go passes nil and the poller skips it.
//
// Compare to Swift: this is like passing a closure `(Int) -> Void` instead
// of defining a protocol with one required method.
type OnGameweekFinalized func(ctx context.Context, eventID int) error

// Config holds the poller's tunable parameters.
type Config struct {
	LeagueID           int
	LeagueType         string        // must be "h2h" — New() returns error otherwise
	IdleInterval       time.Duration // time between polls when idle (default: 6 hours)
	LiveInterval       time.Duration // time between polls when live (default: 15 min)
	ProcessingInterval time.Duration // time between polls when processing (default: 10 min)
}

// Poller implements the adaptive polling state machine.
//
// The lifecycle has three resting states:
//
//	Idle → Live → Processing → (finalization) → Idle
//
// Finalization is a one-shot action, not a state. When the poller detects
// that a gameweek is fully finalized (bonus added, leagues updated), it
// collects data, persists it, fires the callback, and transitions back
// to Idle in a single tick.
type Poller struct {
	fpl         FPLClient
	store       store.Store
	onFinalized OnGameweekFinalized
	cfg         Config
	logger      *slog.Logger

	// --- Mutable state ---
	//
	// These fields change during the polling lifecycle. They are only
	// accessed from the Run loop (single goroutine), so no mutex is needed.

	// state is the current lifecycle phase.
	state State

	// currentEvent is the gameweek we're tracking, derived from bootstrap.
	// nil between seasons or when no gameweek is relevant.
	currentEvent *fpl.Event

	// bootstrap caches the ~1.3MB BootstrapResponse. Refreshed on startup
	// and when the current event changes (i.e., a new GW becomes current).
	bootstrap *fpl.BootstrapResponse

	// lastProcessedEvent is the deduplication guard — the highest eventID
	// we've successfully finalized (data persisted AND callback succeeded).
	// Initialized from the database on startup via GetLatestEventID.
	lastProcessedEvent int

	// rateLimitDelay is the delay between FPL API calls during backfill.
	// Defaults to 500ms. Exposed as a field (not a constant) so unit tests
	// can set it to 0 to avoid wall-clock waits.
	rateLimitDelay time.Duration
}

// New creates a Poller with the given dependencies.
//
// Returns an error if the configuration is invalid. Currently, only H2H
// leagues are supported — passing LeagueType != "h2h" is a fail-fast
// error rather than a silent misconfiguration.
//
// Go pattern — FAIL FAST IN CONSTRUCTORS:
//
// Validate configuration at construction time, not at first use. This
// catches misconfiguration immediately on startup rather than hours later
// when the first gameweek finalizes. In Go, constructors are just regular
// functions that return (T, error) — no special language syntax.
func New(fplClient FPLClient, s store.Store, cfg Config, onFinalized OnGameweekFinalized) (*Poller, error) {
	if cfg.LeagueType != "h2h" {
		return nil, fmt.Errorf("unsupported league type %q: only \"h2h\" is supported", cfg.LeagueType)
	}
	if cfg.IdleInterval <= 0 {
		return nil, fmt.Errorf("IdleInterval must be positive, got %v", cfg.IdleInterval)
	}
	if cfg.LiveInterval <= 0 {
		return nil, fmt.Errorf("LiveInterval must be positive, got %v", cfg.LiveInterval)
	}
	if cfg.ProcessingInterval <= 0 {
		return nil, fmt.Errorf("ProcessingInterval must be positive, got %v", cfg.ProcessingInterval)
	}

	return &Poller{
		fpl:            fplClient,
		store:          s,
		onFinalized:    onFinalized,
		cfg:            cfg,
		logger:         slog.Default(),
		state:          StateIdle,
		rateLimitDelay: 500 * time.Millisecond,
	}, nil
}

// Run starts the polling loop. It blocks until the context is cancelled.
//
// Go pattern — BLOCKING Run WITH CALLER-OWNED GOROUTINE:
//
// Run(ctx) blocks until context cancellation, following the same convention
// as http.ListenAndServe. The caller (main.go) decides whether to run it
// in a goroutine. This gives the caller full control over lifecycle.
//
// Go pattern — time.NewTimer WITH select:
//
// This is the idiomatic interruptible sleep. time.Sleep can't be cancelled
// by context. time.Tick leaks timers (can't be stopped). time.NewTimer
// creates a one-shot timer that select can race against ctx.Done(). When
// the context is cancelled (e.g., SIGTERM), the select case fires
// immediately — no waiting for the timer to expire.
func (p *Poller) Run(ctx context.Context) error {
	// Initialize the deduplication guard from the database on startup.
	// This survives restarts — if the bot was down and comes back, it
	// knows which gameweek was last processed and won't re-process it.
	lastEvent, err := p.store.GetLatestEventID(ctx, int64(p.cfg.LeagueID))
	if err != nil {
		return fmt.Errorf("loading last processed event: %w", err)
	}
	p.lastProcessedEvent = lastEvent

	p.logger.Info("poller starting",
		"league_id", p.cfg.LeagueID,
		"last_processed_event", p.lastProcessedEvent,
		"idle_interval", p.cfg.IdleInterval,
		"live_interval", p.cfg.LiveInterval,
		"processing_interval", p.cfg.ProcessingInterval,
	)

	// First tick runs immediately (no initial delay), then loop with
	// interruptible timers.
	for {
		if err := p.tick(ctx); err != nil {
			// Log but don't exit — transient FPL API errors should not
			// kill the bot. The poller will retry on the next tick.
			p.logger.Error("tick failed", "error", err, "state", p.state)
		}

		interval := p.intervalForState()
		p.logger.Debug("sleeping until next tick",
			"state", p.state,
			"interval", interval,
		)

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Timer fired — continue to next tick.
		}
	}
}

// ---------------------------------------------------------------------------
// State machine core
// ---------------------------------------------------------------------------

// tick executes one polling cycle. It determines the current gameweek state
// and takes appropriate action. Called repeatedly by Run().
func (p *Poller) tick(ctx context.Context) error {
	// Step 1: Refresh bootstrap if needed.
	// During IDLE we always refresh to detect when a new GW becomes current.
	// Otherwise we only fetch it once (on first tick).
	if p.bootstrap == nil || p.state == StateIdle {
		if err := p.refreshBootstrap(ctx); err != nil {
			return fmt.Errorf("refreshing bootstrap: %w", err)
		}
	}

	// Step 2: Determine the relevant event from bootstrap.
	event := p.findRelevantEvent()
	p.currentEvent = event

	// Step 3: No active gameweek → idle.
	if event == nil {
		p.transition(StateIdle)
		p.logger.Debug("no active gameweek found")
		return nil
	}

	// Step 4: Already processed this event → idle.
	if event.ID <= p.lastProcessedEvent {
		p.transition(StateIdle)
		p.logger.Debug("event already processed",
			"event_id", event.ID,
			"last_processed", p.lastProcessedEvent,
		)
		return nil
	}

	// Step 5: Deadline not yet passed → idle.
	deadline, err := time.Parse(time.RFC3339, event.DeadlineTime)
	if err != nil {
		return fmt.Errorf("parsing deadline %q for event %d: %w", event.DeadlineTime, event.ID, err)
	}
	if time.Now().Before(deadline) {
		p.transition(StateIdle)
		p.logger.Debug("deadline not yet passed",
			"event_id", event.ID,
			"deadline", deadline,
		)
		return nil
	}

	// Step 6: Fixtures not finished → live.
	if !event.Finished {
		p.transition(StateLive)
		p.logger.Info("gameweek in progress",
			"event_id", event.ID,
			"event_name", event.Name,
		)
		return nil
	}

	// Step 7: GW is finished — check if finalized via event-status.
	// We only fetch event-status here (not during IDLE/LIVE) to save
	// an unnecessary API call in those states.
	status, err := p.fpl.GetEventStatus(ctx)
	if err != nil {
		return fmt.Errorf("fetching event status: %w", err)
	}

	// Step 8: Check finalization conditions.
	bonusReady := allBonusAdded(status, event.ID)
	if !bonusReady || status.Leagues != "Updated" {
		p.transition(StateProcessing)
		p.logger.Info("waiting for finalization",
			"event_id", event.ID,
			"bonus_added", bonusReady,
			"leagues", status.Leagues,
		)
		return nil
	}

	// Step 9: Gameweek is finalized — collect, persist, notify.
	p.logger.Info("gameweek finalized, starting data collection",
		"event_id", event.ID,
		"event_name", event.Name,
	)

	if err := p.finalize(ctx, event.ID); err != nil {
		// Stay in StateProcessing — retry on next tick.
		// Do NOT advance lastProcessedEvent.
		p.transition(StateProcessing)
		return fmt.Errorf("finalization failed for event %d: %w", event.ID, err)
	}

	// Step 10: Success — advance the guard and return to idle.
	p.lastProcessedEvent = event.ID
	p.transition(StateIdle)

	// Refresh bootstrap to pick up the next gameweek.
	p.bootstrap = nil

	p.logger.Info("gameweek processing complete",
		"event_id", event.ID,
		"event_name", event.Name,
	)
	return nil
}

// ---------------------------------------------------------------------------
// Bootstrap and event detection
// ---------------------------------------------------------------------------

// refreshBootstrap fetches and caches the bootstrap data.
func (p *Poller) refreshBootstrap(ctx context.Context) error {
	resp, err := p.fpl.GetBootstrap(ctx)
	if err != nil {
		return err
	}
	p.bootstrap = &resp
	return nil
}

// findRelevantEvent determines which gameweek the poller should track.
//
// Bootstrap is the source of truth for "which event". The logic:
//  1. Find the event with IsCurrent == true → that's our event.
//  2. If no current event, find IsNext == true and check if its deadline
//     has passed → if so, it just became live.
//  3. If neither applies → nil (off-season / between gameweeks).
func (p *Poller) findRelevantEvent() *fpl.Event {
	if p.bootstrap == nil {
		return nil
	}

	for i := range p.bootstrap.Events {
		if p.bootstrap.Events[i].IsCurrent {
			return &p.bootstrap.Events[i]
		}
	}

	// No current event — check if the "next" event's deadline has passed.
	// This covers the brief window after a deadline passes but before the
	// FPL API updates IsCurrent.
	for i := range p.bootstrap.Events {
		if p.bootstrap.Events[i].IsNext {
			deadline, err := time.Parse(time.RFC3339, p.bootstrap.Events[i].DeadlineTime)
			if err != nil {
				p.logger.Warn("failed to parse next event deadline",
					"event_id", p.bootstrap.Events[i].ID,
					"deadline", p.bootstrap.Events[i].DeadlineTime,
					"error", err,
				)
				return nil
			}
			if time.Now().After(deadline) {
				return &p.bootstrap.Events[i]
			}
			return nil
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Finalization
// ---------------------------------------------------------------------------

// finalize collects all data for a completed gameweek, persists it, and
// fires the callback. This is the core data pipeline:
//
//  1. Fetch standings
//  2. Upsert league (FK prerequisite)
//  3. Upsert managers (FK prerequisite)
//  4. Fetch manager histories
//  5. Map to store types
//  6. Save snapshot (atomic transaction)
//  7. Fire callback (if set)
//
// If ANY step fails — including the callback — the entire finalization is
// considered failed. The poller does NOT advance lastProcessedEvent, so
// it will retry on the next tick.
//
// Why treat callback failure as finalization failure? If we advanced the
// dedup guard after persistence but before a successful callback, a
// transient stats/notifier failure would permanently suppress alerts for
// that gameweek. Since the persistence layer uses idempotent upserts
// (ON CONFLICT), replaying steps 1-6 on retry is safe.
func (p *Poller) finalize(ctx context.Context, eventID int) error {
	leagueID := int64(p.cfg.LeagueID)

	// Step 1: Fetch current standings.
	standingsResp, err := p.fpl.GetAllH2HStandings(ctx, p.cfg.LeagueID)
	if err != nil {
		return fmt.Errorf("fetching standings: %w", err)
	}

	// Step 2: Upsert the league (FK prerequisite for managers and standings).
	league := mapLeague(standingsResp.League, p.cfg.LeagueType)
	if err := p.store.UpsertLeague(ctx, league); err != nil {
		return fmt.Errorf("upserting league: %w", err)
	}

	// Step 3: Upsert all managers (FK prerequisite for standings and chips).
	for _, entry := range standingsResp.Standings.Results {
		manager := mapManager(leagueID, entry)
		if err := p.store.UpsertManager(ctx, manager); err != nil {
			return fmt.Errorf("upserting manager %d: %w", entry.EntryID, err)
		}
	}

	// Step 4: Fetch manager histories and collect chip usages.
	// Each manager's history endpoint returns ALL chips across ALL gameweeks,
	// so mapChipUsages filters to only this eventID.
	var allChips []store.ChipUsage
	for _, entry := range standingsResp.Standings.Results {
		history, err := p.fpl.GetManagerHistory(ctx, entry.EntryID)
		if err != nil {
			return fmt.Errorf("fetching history for manager %d: %w", entry.EntryID, err)
		}

		chips := mapChipUsages(leagueID, eventID, int64(entry.EntryID), history.Chips)
		allChips = append(allChips, chips...)
	}

	// Step 5: Map standings to store types.
	standings := mapStandings(leagueID, eventID, standingsResp.Standings.Results)

	// Step 6: Save the snapshot atomically.
	// Pass nil for H2H results — the /leagues-h2h/{id}/matches/ endpoint
	// returned 404 during development. The store handles nil slices fine.
	if err := p.store.SaveGameweekSnapshot(ctx, standings, allChips, nil); err != nil {
		return fmt.Errorf("saving snapshot: %w", err)
	}

	// Tag this snapshot as live data with historical fidelity.
	// The stats engine (Phase 1.5) can use this to distinguish live
	// snapshots from backfilled ones where standings are synthetic.
	meta := store.SnapshotMeta{
		LeagueID:          leagueID,
		EventID:           eventID,
		Source:            "live",
		StandingsFidelity: "historical",
	}
	if err := p.store.UpsertSnapshotMeta(ctx, meta); err != nil {
		return fmt.Errorf("upserting snapshot meta: %w", err)
	}

	p.logger.Info("snapshot saved",
		"event_id", eventID,
		"managers", len(standings),
		"chips", len(allChips),
	)

	// Step 7: Fire the callback (if set).
	if p.onFinalized != nil {
		if err := p.onFinalized(ctx, eventID); err != nil {
			return fmt.Errorf("onFinalized callback: %w", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// allBonusAdded checks whether all matchday entries for the given event
// have had bonus points added. A gameweek spans multiple matchdays (e.g.,
// Saturday, Sunday, Monday), each with its own EventStatus entry.
//
// Returns false if no status entries exist for the event — this handles
// the off-season case where the status slice may be empty.
func allBonusAdded(resp fpl.EventStatusResponse, eventID int) bool {
	found := false
	for _, s := range resp.Status {
		if s.Event == eventID {
			found = true
			if !s.BonusAdded {
				return false
			}
		}
	}
	return found
}

// transition updates the poller's state and logs the change.
func (p *Poller) transition(newState State) {
	if p.state != newState {
		p.logger.Info("state transition",
			"from", p.state,
			"to", newState,
		)
		p.state = newState
	}
}

// intervalForState returns the poll interval for the current state.
func (p *Poller) intervalForState() time.Duration {
	switch p.state {
	case StateLive:
		return p.cfg.LiveInterval
	case StateProcessing:
		return p.cfg.ProcessingInterval
	default:
		return p.cfg.IdleInterval
	}
}
