package poller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// Backfill populates and enriches the current season's finished gameweeks.
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
// once when snapshot or manager-stat enrichment is needed, then reuse
// that data across current-season gameweeks.
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

	// Step 3: Load current coverage for snapshots, manager stats, and awards.
	leagueID := int64(p.cfg.LeagueID)
	storedIDs, err := p.store.GetStoredEventIDs(ctx, leagueID)
	if err != nil {
		return fmt.Errorf("getting stored event IDs: %w", err)
	}
	managerStatIDs, err := p.store.GetStoredManagerStatEventIDs(ctx, leagueID)
	if err != nil {
		return fmt.Errorf("getting stored manager stat event IDs: %w", err)
	}
	awardIDs, err := p.store.GetStoredAwardEventIDs(ctx, leagueID)
	if err != nil {
		return fmt.Errorf("getting stored award event IDs: %w", err)
	}

	storedSet := idsToSet(storedIDs)
	managerStatSet := idsToSet(managerStatIDs)
	awardSet := idsToSet(awardIDs)

	// Step 4: Compute current-season gaps.
	var (
		missingSnapshots    []int
		missingManagerStats []int
		missingAwards       []int
	)
	for id := range finishedSet {
		if !storedSet[id] {
			missingSnapshots = append(missingSnapshots, id)
			continue
		}
		if !managerStatSet[id] {
			missingManagerStats = append(missingManagerStats, id)
			continue
		}
		if !awardSet[id] {
			missingAwards = append(missingAwards, id)
		}
	}
	sort.Ints(missingSnapshots)
	sort.Ints(missingManagerStats)
	sort.Ints(missingAwards)

	if len(missingSnapshots) == 0 && len(missingManagerStats) == 0 && len(missingAwards) == 0 {
		p.logger.Info("no backfill needed — current-season finished gameweeks already have snapshots, manager stats, and awards")
		return nil
	}

	p.logger.Info("starting current-season backfill/enrichment",
		"missing_snapshots", len(missingSnapshots),
		"missing_manager_stats", len(missingManagerStats),
		"missing_awards", len(missingAwards),
	)
	if len(missingSnapshots) > 0 {
		p.logger.Warn("backfilled standings reflect current cumulative values, not historical — tagged as synthetic")
	}

	needManagerData := len(missingSnapshots) > 0 || len(missingManagerStats) > 0
	var (
		standingsResp fpl.H2HStandingsResponse
		histories     map[int]fpl.ManagerHistoryResponse
	)
	if needManagerData {
		// Step 5: Fetch standings once — reused for all snapshot/stat enrichments.
		standingsResp, err = p.fpl.GetAllH2HStandings(ctx, p.cfg.LeagueID)
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

		// Step 7: Fetch each manager's season history once.
		histories, err = p.fetchManagerHistories(ctx, standingsResp.Standings.Results)
		if err != nil {
			return err
		}
	}

	processed := 0
	total := len(missingSnapshots) + len(missingManagerStats) + len(missingAwards)

	for _, eventID := range missingSnapshots {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Map standings for this GW (same standings data, different eventID).
		standings := mapStandings(leagueID, eventID, standingsResp.Standings.Results)

		allChips, err := chipsForEvent(leagueID, eventID, standingsResp.Standings.Results, histories)
		if err != nil {
			return err
		}
		managerStats, err := p.buildManagerStatsForEvent(ctx, leagueID, eventID, standingsResp.Standings.Results, histories)
		if err != nil {
			return err
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

		snap := store.GameweekSnapshot{
			Standings:    standings,
			Chips:        allChips,
			Results:      results,
			ManagerStats: managerStats,
			Meta: store.SnapshotMeta{
				LeagueID:          leagueID,
				EventID:           eventID,
				Source:            "backfill",
				StandingsFidelity: "synthetic",
			},
		}
		if err := p.store.SaveGameweekSnapshot(ctx, snap); err != nil {
			return fmt.Errorf("saving snapshot for GW %d: %w", eventID, err)
		}
		if p.onFinalized != nil {
			if err := p.onFinalized(ctx, eventID); err != nil {
				return fmt.Errorf("computing awards for GW %d after snapshot backfill: %w", eventID, err)
			}
		}

		processed++
		p.logger.Info("backfilled gameweek",
			"event_id", eventID,
			"progress", fmt.Sprintf("%d/%d", processed, total),
		)
	}

	for _, eventID := range missingManagerStats {
		if err := ctx.Err(); err != nil {
			return err
		}

		managerStats, err := p.buildManagerStatsForEvent(ctx, leagueID, eventID, standingsResp.Standings.Results, histories)
		if err != nil {
			return err
		}
		meta, err := p.store.GetSnapshotMeta(ctx, leagueID, eventID)
		if err != nil {
			return fmt.Errorf("getting snapshot meta for GW %d: %w", eventID, err)
		}

		if err := p.store.SaveGameweekSnapshot(ctx, store.GameweekSnapshot{
			ManagerStats: managerStats,
			Meta:         meta,
		}); err != nil {
			return fmt.Errorf("saving manager stats for GW %d: %w", eventID, err)
		}
		if p.onFinalized != nil {
			if err := p.onFinalized(ctx, eventID); err != nil {
				return fmt.Errorf("computing awards for GW %d after manager-stat enrichment: %w", eventID, err)
			}
		}

		processed++
		p.logger.Info("enriched gameweek manager stats",
			"event_id", eventID,
			"progress", fmt.Sprintf("%d/%d", processed, total),
		)
	}

	for _, eventID := range missingAwards {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.onFinalized == nil {
			p.logger.Warn("skipping award recomputation because no onFinalized callback is configured",
				"event_id", eventID,
			)
			continue
		}
		if err := p.onFinalized(ctx, eventID); err != nil {
			return fmt.Errorf("computing awards for GW %d: %w", eventID, err)
		}

		processed++
		p.logger.Info("recomputed gameweek awards",
			"event_id", eventID,
			"progress", fmt.Sprintf("%d/%d", processed, total),
		)
	}

	p.logger.Info("backfill complete", "total", processed)
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
