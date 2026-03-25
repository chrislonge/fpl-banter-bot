package poller

import (
	"context"
	"fmt"

	"github.com/chrislonge/fpl-banter-bot/internal/fpl"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
)

func (p *Poller) fetchManagerHistories(ctx context.Context, entries []fpl.StandingEntry) (map[int]fpl.ManagerHistoryResponse, error) {
	histories := make(map[int]fpl.ManagerHistoryResponse, len(entries))
	for _, entry := range entries {
		if err := rateLimitDelay(ctx, p.rateLimitDelay); err != nil {
			return nil, err
		}
		history, err := p.fpl.GetManagerHistory(ctx, entry.EntryID)
		if err != nil {
			return nil, fmt.Errorf("fetching history for manager %d: %w", entry.EntryID, err)
		}
		histories[entry.EntryID] = history
	}
	return histories, nil
}

func chipsForEvent(leagueID int64, eventID int, entries []fpl.StandingEntry, histories map[int]fpl.ManagerHistoryResponse) ([]store.ChipUsage, error) {
	var allChips []store.ChipUsage
	for _, entry := range entries {
		history, ok := histories[entry.EntryID]
		if !ok {
			return nil, fmt.Errorf("manager %d history missing from cache", entry.EntryID)
		}
		chips := mapChipUsages(leagueID, eventID, int64(entry.EntryID), history.Chips)
		allChips = append(allChips, chips...)
	}
	return allChips, nil
}

func (p *Poller) buildManagerStatsForEvent(ctx context.Context, leagueID int64, eventID int, entries []fpl.StandingEntry, histories map[int]fpl.ManagerHistoryResponse) ([]store.GameweekManagerStat, error) {
	if err := rateLimitDelay(ctx, p.rateLimitDelay); err != nil {
		return nil, err
	}
	liveResp, err := p.fpl.GetEventLive(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("fetching event live data for GW %d: %w", eventID, err)
	}
	livePoints := livePointsByElement(liveResp)

	stats := make([]store.GameweekManagerStat, 0, len(entries))
	for _, entry := range entries {
		history, ok := histories[entry.EntryID]
		if !ok {
			return nil, fmt.Errorf("manager %d history missing from cache", entry.EntryID)
		}

		gwHistory, ok := findGameweekHistory(history.Current, eventID)
		if !ok {
			return nil, fmt.Errorf("manager %d: no history row for GW %d", entry.EntryID, eventID)
		}

		if err := rateLimitDelay(ctx, p.rateLimitDelay); err != nil {
			return nil, err
		}
		picksResp, err := p.fpl.GetManagerPicks(ctx, entry.EntryID, eventID)
		if err != nil {
			return nil, fmt.Errorf("fetching picks for manager %d in GW %d: %w", entry.EntryID, eventID, err)
		}

		stat, err := mapGameweekManagerStat(leagueID, eventID, int64(entry.EntryID), gwHistory.PointsOnBench, picksResp.Picks, livePoints)
		if err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	return stats, nil
}

func idsToSet(ids []int) map[int]bool {
	set := make(map[int]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
