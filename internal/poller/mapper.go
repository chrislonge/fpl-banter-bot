// Package poller implements the gameweek lifecycle state machine.
//
// The poller is the coordinator that drives the bot — it polls the FPL API
// on a schedule, detects gameweek lifecycle transitions, collects data when
// a gameweek finalizes, persists it, and fires a callback for the stats
// engine. The poller knows about the FPL client and the store, but knows
// nothing about stats or Telegram (Single Responsibility Principle).
package poller

import (
	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// ---------------------------------------------------------------------------
// Mapper functions — pure functions that convert FPL API types to store types
// ---------------------------------------------------------------------------
//
// These live in a separate file because they are pure functions with no side
// effects and no dependency on the Poller struct. This makes them trivially
// testable — no fakes, no setup, just input → output.
//
// Go concept — BOUNDARY MAPPING:
//
// The FPL API types (internal/fpl/) and the store types (internal/store/) are
// intentionally separate. Each package defines its own domain model:
//   - fpl types match the JSON shapes from the API
//   - store types match the database schema
//
// The poller sits at the boundary between these two packages. These mapper
// functions perform the translation. This is the Anti-Corruption Layer pattern
// from Domain-Driven Design — it prevents the internal representation of one
// layer from leaking into another.

// mapLeague converts FPL league metadata to a store League.
//
// The LeagueInfo from the API doesn't include the league type (h2h vs classic)
// — that comes from our configuration. So leagueType is passed separately.
func mapLeague(info fpl.LeagueInfo, leagueType string) store.League {
	return store.League{
		ID:   int64(info.ID),
		Name: info.Name,
		Type: leagueType,
	}
}

// mapManager converts a single FPL standing entry to a store Manager.
//
// Note the int → int64 type conversion. The FPL API uses plain int for IDs,
// but the store uses int64 to match the Postgres BIGINT column type. In Go,
// int(42) and int64(42) are different types — you must explicitly convert.
// This is stricter than Swift/Kotlin where Int and Long have implicit
// conversions in some contexts.
func mapManager(leagueID int64, entry fpl.StandingEntry) store.Manager {
	return store.Manager{
		LeagueID: leagueID,
		ID:       int64(entry.EntryID),
		Name:     entry.PlayerName,
		TeamName: entry.EntryName,
	}
}

// mapStandings converts a slice of FPL standing entries to store GameweekStandings.
//
// Key field mapping:
//   - entry.Total  → Points    (H2H points: W=3, D=1, L=0)
//   - entry.PointsFor → TotalScore (cumulative FPL points scored)
//
// These names differ because the FPL API and our schema use different
// terminology for the same concept.
func mapStandings(leagueID int64, eventID int, entries []fpl.StandingEntry) []store.GameweekStanding {
	standings := make([]store.GameweekStanding, len(entries))
	for i, e := range entries {
		standings[i] = store.GameweekStanding{
			LeagueID:   leagueID,
			EventID:    eventID,
			ManagerID:  int64(e.EntryID),
			Rank:       e.Rank,
			Points:     e.Total,
			TotalScore: e.PointsFor,
		}
	}
	return standings
}

// mapChipUsages converts a manager's chip history to store ChipUsage records,
// filtered to only include chips used in the specified eventID.
//
// CRITICAL: The manager history endpoint returns ALL chips across ALL
// gameweeks — not just the current one. Without filtering, we'd insert
// duplicate chip records for every previously-used chip on every gameweek
// finalization. The ON CONFLICT DO NOTHING clause in the store would
// prevent actual duplicates, but it's cleaner and more efficient to
// filter at the mapping layer.
func mapChipUsages(leagueID int64, eventID int, managerID int64, chips []fpl.ChipUsage) []store.ChipUsage {
	var filtered []store.ChipUsage
	for _, c := range chips {
		if c.Event != eventID {
			continue
		}
		filtered = append(filtered, store.ChipUsage{
			LeagueID:  leagueID,
			ManagerID: managerID,
			EventID:   eventID,
			Chip:      c.Name,
		})
	}
	return filtered
}

// mapH2HResults converts FPL match rows into store H2HResult rows.
//
// The store schema enforces canonical ordering (manager_1_id < manager_2_id),
// so we sort the pair here and swap the scores with them when needed.
//
// Bye rows are skipped because the current schema models only true two-manager
// fixtures. Standard H2H league weeks for the Capital FC league do not use
// byes, and cup fixtures are out of scope for this phase.
func mapH2HResults(leagueID int64, matches []fpl.H2HMatch) []store.H2HResult {
	results := make([]store.H2HResult, 0, len(matches))
	for _, m := range matches {
		if m.IsBye || m.Entry1Entry == 0 || m.Entry2Entry == 0 {
			continue
		}

		manager1ID := int64(m.Entry1Entry)
		manager1Score := m.Entry1Points
		manager2ID := int64(m.Entry2Entry)
		manager2Score := m.Entry2Points

		if manager1ID > manager2ID {
			manager1ID, manager2ID = manager2ID, manager1ID
			manager1Score, manager2Score = manager2Score, manager1Score
		}

		results = append(results, store.H2HResult{
			LeagueID:      leagueID,
			EventID:       m.Event,
			Manager1ID:    manager1ID,
			Manager1Score: manager1Score,
			Manager2ID:    manager2ID,
			Manager2Score: manager2Score,
		})
	}
	return results
}
