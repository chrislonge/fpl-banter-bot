package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

// handleStandings fetches the latest standings and returns a formatted table.
//
// Go pattern — FREE FUNCTION WITH EXPLICIT DEPS:
//
// Each command handler is a free function (no receiver) that takes only the
// dependencies it needs. This is more testable than a method on a God object
// because the test only needs to fake the specific dependencies used by this
// one handler. In Swift/Kotlin you'd achieve this with protocol extensions
// or standalone functions — the principle is the same: minimize coupling.
func handleStandings(ctx context.Context, s LeagueStore, leagueID int64) (string, error) {
	eventID, err := s.GetLatestEventID(ctx, leagueID)
	if err != nil {
		return "", fmt.Errorf("get latest event: %w", err)
	}
	if eventID == 0 {
		return "No gameweek data available yet.", nil
	}

	standings, err := s.GetStandings(ctx, leagueID, eventID)
	if err != nil {
		return "", fmt.Errorf("get standings: %w", err)
	}

	managers, err := s.GetManagers(ctx, leagueID)
	if err != nil {
		return "", fmt.Errorf("get managers: %w", err)
	}

	return formatStandings(eventID, standings, managers), nil
}

// handleStreak fetches current streaks and returns a formatted list.
func handleStreak(ctx context.Context, sq StatsQuerier) (string, error) {
	streaks, err := sq.GetCurrentStreaks(ctx)
	if err != nil {
		return "", fmt.Errorf("get streaks: %w", err)
	}
	return formatStreaks(streaks), nil
}

// handleHistory resolves two manager arguments, fetches their head-to-head
// record, and returns a formatted summary.
//
// Manager resolution: each argument is tried as a rank (integer) first, then
// as a case-insensitive name prefix. This mirrors how you'd casually refer to
// a manager in a group chat — "1" for the league leader, or "Chr" for Chris.
func handleHistory(ctx context.Context, sq StatsQuerier, s LeagueStore, leagueID int64, args []string) (string, error) {
	if len(args) < 2 {
		return "Usage: /history <manager1> <manager2>\nUse rank number or name prefix.", nil
	}

	// Fetch standings and managers upfront — both are needed for resolution.
	eventID, err := s.GetLatestEventID(ctx, leagueID)
	if err != nil {
		return "", fmt.Errorf("get latest event: %w", err)
	}
	if eventID == 0 {
		return "No gameweek data available yet.", nil
	}

	standings, err := s.GetStandings(ctx, leagueID, eventID)
	if err != nil {
		return "", fmt.Errorf("get standings: %w", err)
	}

	managers, err := s.GetManagers(ctx, leagueID)
	if err != nil {
		return "", fmt.Errorf("get managers: %w", err)
	}

	managerA, err := resolveManager(args[0], managers, standings)
	if err != nil {
		return fmt.Sprintf("Could not resolve %q: %s", esc(args[0]), esc(err.Error())), nil
	}

	managerB, err := resolveManager(args[1], managers, standings)
	if err != nil {
		return fmt.Sprintf("Could not resolve %q: %s", esc(args[1]), esc(err.Error())), nil
	}

	if managerA.ID == managerB.ID {
		return "That's the same manager!", nil
	}

	record, err := sq.GetH2HRecord(ctx, managerA.ID, managerB.ID)
	if err != nil {
		return "", fmt.Errorf("get h2h record: %w", err)
	}

	return formatH2HRecord(record), nil
}

// handleDeadline fetches the bootstrap data and finds the next gameweek
// deadline to display.
func handleDeadline(ctx context.Context, fq FPLQuerier) (string, error) {
	bootstrap, err := fq.GetBootstrap(ctx)
	if err != nil {
		return "", fmt.Errorf("get bootstrap: %w", err)
	}

	// Find the event marked IsNext — this is the upcoming gameweek whose
	// deadline hasn't passed yet.
	for _, event := range bootstrap.Events {
		if !event.IsNext {
			continue
		}

		deadline, err := time.Parse(time.RFC3339, event.DeadlineTime)
		if err != nil {
			return "", fmt.Errorf("parsing deadline for %s: %w", event.Name, err)
		}

		return formatDeadline(event.Name, deadline), nil
	}

	return "No upcoming deadline found. The season may be over.", nil
}

// resolveManager tries to identify a manager from a user-provided argument.
//
// Resolution strategy (ordered):
//  1. Try parsing as an integer rank. If it matches a rank in the current
//     standings, return that manager.
//  2. Try case-insensitive prefix matching against manager names.
//     - Exactly one match: return it.
//     - Zero matches: return a "not found" error.
//     - Multiple matches: return an "ambiguous" error listing the matches.
//
// This two-step strategy lets users type either "1" (rank) or "Chr" (name
// prefix) in the group chat — both are natural ways to refer to a manager.
func resolveManager(arg string, managers []store.Manager, standings []store.GameweekStanding) (store.Manager, error) {
	// Step 1: try rank integer.
	if rank, err := strconv.Atoi(arg); err == nil {
		// Build a lookup from rank → manager ID using current standings.
		for _, s := range standings {
			if s.Rank == rank {
				// Find the manager entry for this ID.
				for _, m := range managers {
					if m.ID == s.ManagerID {
						return m, nil
					}
				}
			}
		}
		// The rank was a valid integer but didn't match anyone.
		return store.Manager{}, fmt.Errorf("no manager at rank %d", rank)
	}

	// Step 2: case-insensitive name prefix.
	prefix := strings.ToLower(arg)
	var matches []store.Manager
	for _, m := range managers {
		if strings.HasPrefix(strings.ToLower(m.Name), prefix) {
			matches = append(matches, m)
		}
	}

	switch len(matches) {
	case 0:
		return store.Manager{}, fmt.Errorf("no manager matching %q", arg)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return store.Manager{}, fmt.Errorf("ambiguous: matches %s", strings.Join(names, ", "))
	}
}
