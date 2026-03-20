package poller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// Backfill populates the database with historical gameweek data for any
// finished gameweeks that don't already have snapshots stored.
//
// This is a one-time recovery operation for when the bot was deployed
// mid-season. For example, if the bot started at GW30, backfill will
// populate GWs 1-29 so the stats engine has complete records.
//
// Go idiom — SEPARATE ENTRYPOINT FOR ONE-TIME OPERATIONS:
//
// Backfill is not called automatically at startup. It's invoked via a
// separate binary (cmd/backfill/main.go). This keeps the poller's Run()
// contract as a resilient long-running loop — a flaky FPL API during
// backfill doesn't prevent the bot from starting. Go projects commonly
// have multiple binaries under cmd/, each sharing internal/ packages.
//
// KEY LIMITATION: The FPL API's standings endpoint returns current
// cumulative values, not historical point-in-time snapshots. Backfilled
// standings will show GW30 ranks/points for all gameweeks. Chip usage
// and H2H match results ARE historical. The gameweek_snapshot_meta table
// tags backfilled snapshots as "synthetic" so the stats engine knows not
// to trust rank diffs from these gameweeks.
//
// API OPTIMIZATION: We fetch standings once and each manager's history
// once, then iterate over missing gameweeks. For a 14-manager league,
// that's 15 API calls total regardless of how many GWs need backfilling.
func (p *Poller) Backfill(ctx context.Context) error {
	// Step 1: Refresh bootstrap to know which events exist and are finished.
	if err := p.refreshBootstrap(ctx); err != nil {
		return fmt.Errorf("refreshing bootstrap: %w", err)
	}

	// Step 2: Build the set of finished event IDs from bootstrap.
	finishedSet := make(map[int]bool)
	for _, e := range p.bootstrap.Events {
		if e.Finished {
			finishedSet[e.ID] = true
		}
	}
	if len(finishedSet) == 0 {
		p.logger.Info("no finished events found — nothing to backfill")
		return nil
	}

	// Step 3: Get already-stored event IDs from the database.
	//
	// Go idiom — SET DIFFERENCE INSTEAD OF MAX:
	//
	// We compute missing = finishedSet - storedSet rather than using
	// MAX(event_id) + 1. This correctly handles:
	//   - DB has only GW30 → backfills 1-29
	//   - DB has GW1, 2, 5 → backfills 3, 4 (plus any other finished GWs)
	//   - DB has all GWs → no-op
	leagueID := int64(p.cfg.LeagueID)
	storedIDs, err := p.store.GetStoredEventIDs(ctx, leagueID)
	if err != nil {
		return fmt.Errorf("getting stored event IDs: %w", err)
	}
	storedSet := make(map[int]bool, len(storedIDs))
	for _, id := range storedIDs {
		storedSet[id] = true
	}

	// Step 4: Compute the missing event IDs.
	var missing []int
	for id := range finishedSet {
		if !storedSet[id] {
			missing = append(missing, id)
		}
	}
	sort.Ints(missing)

	if len(missing) == 0 {
		p.logger.Info("no backfill needed — all finished gameweeks already stored")
		return nil
	}

	p.logger.Info("starting backfill",
		"missing_gameweeks", len(missing),
		"first", missing[0],
		"last", missing[len(missing)-1],
	)
	p.logger.Warn("backfilled standings reflect current cumulative values, not historical — tagged as synthetic")

	// Step 5: Fetch standings once — reused for all missing GWs.
	standingsResp, err := p.fpl.GetAllH2HStandings(ctx, p.cfg.LeagueID)
	if err != nil {
		return fmt.Errorf("fetching standings: %w", err)
	}

	if err := rateLimitDelay(ctx, p.rateLimitDelay); err != nil {
		return err
	}

	// Step 6: Upsert league + managers (FK prerequisites).
	league := mapLeague(standingsResp.League, p.cfg.LeagueType)
	if err := p.store.UpsertLeague(ctx, league); err != nil {
		return fmt.Errorf("upserting league: %w", err)
	}
	for _, entry := range standingsResp.Standings.Results {
		manager := mapManager(leagueID, entry)
		if err := p.store.UpsertManager(ctx, manager); err != nil {
			return fmt.Errorf("upserting manager %d: %w", entry.EntryID, err)
		}
	}

	// Step 7: Fetch each manager's history once — extract chips per GW later.
	//
	// Go concept — MAP LITERAL TYPE:
	//
	// histories is a map from manager ID (int) to the full history response.
	// We fetch once per manager and then extract chips for each GW in the
	// loop below. This avoids N*M API calls (N managers * M gameweeks).
	histories := make(map[int]fpl.ManagerHistoryResponse, len(standingsResp.Standings.Results))
	for _, entry := range standingsResp.Standings.Results {
		if err := rateLimitDelay(ctx, p.rateLimitDelay); err != nil {
			return err
		}
		history, err := p.fpl.GetManagerHistory(ctx, entry.EntryID)
		if err != nil {
			return fmt.Errorf("fetching history for manager %d: %w", entry.EntryID, err)
		}
		histories[entry.EntryID] = history
	}

	// Step 8: Iterate over missing GWs and persist snapshots.
	for i, eventID := range missing {
		// Check for graceful shutdown between iterations.
		if err := ctx.Err(); err != nil {
			return err
		}

		// Map standings for this GW (same standings data, different eventID).
		standings := mapStandings(leagueID, eventID, standingsResp.Standings.Results)

		// Collect chips from all managers for this specific GW.
		var allChips []store.ChipUsage
		for _, entry := range standingsResp.Standings.Results {
			history := histories[entry.EntryID]
			chips := mapChipUsages(leagueID, eventID, int64(entry.EntryID), history.Chips)
			allChips = append(allChips, chips...)
		}

		// Fetch and map the event's actual H2H fixtures.
		if err := rateLimitDelay(ctx, p.rateLimitDelay); err != nil {
			return err
		}
		matchesResp, err := p.fpl.GetAllH2HMatches(ctx, p.cfg.LeagueID, eventID)
		if err != nil {
			return fmt.Errorf("fetching h2h matches for GW %d: %w", eventID, err)
		}
		results := mapH2HResults(leagueID, eventID, matchesResp.Results)

		// Persist the snapshot atomically (standings + chips + results + meta).
		meta := store.SnapshotMeta{
			LeagueID:          leagueID,
			EventID:           eventID,
			Source:            "backfill",
			StandingsFidelity: "synthetic",
		}
		if err := p.store.SaveGameweekSnapshot(ctx, standings, allChips, results, meta); err != nil {
			return fmt.Errorf("saving snapshot for GW %d: %w", eventID, err)
		}

		p.logger.Info("backfilled gameweek",
			"event_id", eventID,
			"progress", fmt.Sprintf("%d/%d", i+1, len(missing)),
		)
	}

	p.logger.Info("backfill complete", "total", len(missing))
	return nil
}

// rateLimitDelay sleeps for the given duration, but returns immediately
// with ctx.Err() if the context is cancelled.
//
// Go idiom — time.NewTimer (NOT time.After) IN A LOOP:
//
// time.After creates a channel that isn't garbage-collected until the
// timer fires, even if you select on ctx.Done() first. In a loop, this
// leaks timers. time.NewTimer gives you a Stop() method to clean up
// the timer when the context wins the race.
//
// This is the same pattern used in the poller's Run() loop — select
// on either the timer or ctx.Done(), whichever comes first.
func rateLimitDelay(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
