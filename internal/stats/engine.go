package stats

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

const historicalStandings = "historical"

// Store defines the read-side data the stats engine needs.
//
// The consumer defines this interface, not the producer. That keeps the stats
// package decoupled from write-only store methods and makes test fakes tiny.
type Store interface {
	GetStandings(ctx context.Context, leagueID int64, eventID int) ([]store.GameweekStanding, error)
	GetChipUsage(ctx context.Context, leagueID int64, eventID int) ([]store.ChipUsage, error)
	GetH2HResults(ctx context.Context, leagueID int64, eventID int) ([]store.H2HResult, error)
	GetH2HResultsRange(ctx context.Context, leagueID int64, fromEvent int, toEvent int) ([]store.H2HResult, error)
	GetGameweekManagerStats(ctx context.Context, leagueID int64, eventID int) ([]store.GameweekManagerStat, error)
	GetManagers(ctx context.Context, leagueID int64) ([]store.Manager, error)
	GetLatestEventID(ctx context.Context, leagueID int64) (int, error)
	GetSnapshotMeta(ctx context.Context, leagueID int64, eventID int) (store.SnapshotMeta, error)
	SaveGameweekAwards(ctx context.Context, leagueID int64, eventID int, awards []store.GameweekAward) error
}

// PlayerLookup provides optional current-season player display names.
// Awards fall back gracefully when this data is unavailable.
type PlayerLookup interface {
	PlayerNames(ctx context.Context) (map[int]notify.PlayerRef, error)
}

// PlayerLookupFunc adapts a function to the PlayerLookup interface.
type PlayerLookupFunc func(ctx context.Context) (map[int]notify.PlayerRef, error)

func (f PlayerLookupFunc) PlayerNames(ctx context.Context) (map[int]notify.PlayerRef, error) {
	return f(ctx)
}

// Engine computes alert-worthy stats for a single league.
type Engine struct {
	store        Store
	leagueID     int64
	playerLookup PlayerLookup
}

// CurrentStreak describes a manager's active win or loss streak.
type CurrentStreak struct {
	Manager    notify.ManagerRef
	Kind       notify.StreakKind
	Length     int
	StartedAt  int
	FinishedAt int
}

// H2HRecord aggregates the lifetime head-to-head record between two managers.
type H2HRecord struct {
	LeagueID      int64
	ManagerA      notify.ManagerRef
	ManagerB      notify.ManagerRef
	GamesPlayed   int
	ManagerAWins  int
	ManagerBWins  int
	Draws         int
	ManagerAScore int
	ManagerBScore int
}

type eventOutcome struct {
	EventID int
	Kind    notify.StreakKind
}

// New constructs a stats engine for one league.
func New(store Store, leagueID int64) *Engine {
	return NewWithPlayerLookup(store, leagueID, nil)
}

// NewWithPlayerLookup constructs a stats engine with optional player lookup.
func NewWithPlayerLookup(store Store, leagueID int64, playerLookup PlayerLookup) *Engine {
	return &Engine{
		store:        store,
		leagueID:     leagueID,
		playerLookup: playerLookup,
	}
}

// BuildGameweekAlerts computes all proactive alerts for a single gameweek.
func (e *Engine) BuildGameweekAlerts(ctx context.Context, eventID int) ([]notify.Alert, error) {
	return e.buildGameweekAlerts(ctx, eventID, true)
}

// BuildGameweekAlertsReadOnly computes proactive alerts without persisting
// any derived award rows. This is used by diagnostic tooling such as
// notify-test verification passes.
func (e *Engine) BuildGameweekAlertsReadOnly(ctx context.Context, eventID int) ([]notify.Alert, error) {
	return e.buildGameweekAlerts(ctx, eventID, false)
}

func (e *Engine) buildGameweekAlerts(ctx context.Context, eventID int, persistAwards bool) ([]notify.Alert, error) {
	managerByID, err := e.getManagerDirectory(ctx)
	if err != nil {
		return nil, err
	}

	currentStandings, err := e.store.GetStandings(ctx, e.leagueID, eventID)
	if err != nil {
		return nil, fmt.Errorf("get standings for event %d: %w", eventID, err)
	}

	currentChips, err := e.store.GetChipUsage(ctx, e.leagueID, eventID)
	if err != nil {
		return nil, fmt.Errorf("get chip usage for event %d: %w", eventID, err)
	}

	currentResults, err := e.store.GetH2HResults(ctx, e.leagueID, eventID)
	if err != nil {
		return nil, fmt.Errorf("get h2h results for event %d: %w", eventID, err)
	}

	currentManagerStats, err := e.store.GetGameweekManagerStats(ctx, e.leagueID, eventID)
	if err != nil {
		return nil, fmt.Errorf("get manager stats for event %d: %w", eventID, err)
	}

	playerByID := e.getPlayerDirectory(ctx)

	alerts := make([]notify.Alert, 0, len(currentStandings)+len(currentChips)+len(currentResults)+3)

	currentHistorical, err := e.hasHistoricalStandings(ctx, eventID)
	if err != nil {
		return nil, err
	}

	prevRanks := map[int64]int{}
	prevHistorical := false
	if eventID > 1 {
		prevRanks, prevHistorical, err = e.getHistoricalRanks(ctx, eventID-1)
		if err != nil {
			return nil, err
		}
	}

	if eventID > 1 && currentHistorical && prevHistorical {
		for _, standing := range currentStandings {
			prevRank, ok := prevRanks[standing.ManagerID]
			if !ok || prevRank == standing.Rank {
				continue
			}

			manager := managerRef(managerByID, standing.ManagerID)
			alerts = append(alerts, notify.Alert{
				Kind:     notify.AlertKindRankChange,
				LeagueID: e.leagueID,
				EventID:  eventID,
				RankChange: &notify.RankChangeAlert{
					Manager:        manager,
					PreviousRank:   prevRank,
					CurrentRank:    standing.Rank,
					MovedIntoFirst: standing.Rank == 1 && prevRank != 1,
				},
			})
		}
	}

	if eventID >= 1 {
		historyResults, err := e.store.GetH2HResultsRange(ctx, e.leagueID, 1, eventID)
		if err != nil {
			return nil, fmt.Errorf("get h2h result history through event %d: %w", eventID, err)
		}

		playedThisWeek := managersInResults(currentResults)
		for _, streak := range buildCurrentStreaks(managerByID, historyResults) {
			if streak.Length < 3 {
				continue
			}
			if _, ok := playedThisWeek[streak.Manager.ID]; !ok {
				continue
			}

			alerts = append(alerts, notify.Alert{
				Kind:     notify.AlertKindStreak,
				LeagueID: e.leagueID,
				EventID:  eventID,
				Streak: &notify.StreakAlert{
					Manager:    streak.Manager,
					Kind:       streak.Kind,
					Length:     streak.Length,
					StartedAt:  streak.StartedAt,
					FinishedAt: streak.FinishedAt,
				},
			})
		}
	}

	for _, chip := range currentChips {
		alerts = append(alerts, notify.Alert{
			Kind:     notify.AlertKindChipUsage,
			LeagueID: e.leagueID,
			EventID:  eventID,
			ChipUsage: &notify.ChipUsageAlert{
				Manager: managerRef(managerByID, chip.ManagerID),
				Chip:    chip.Chip,
			},
		})
	}

	plotTwist := buildPlotTwist(managerByID, currentResults, prevRanks, prevHistorical)

	awardsAlert, awardRows := buildGameweekAwards(e.leagueID, eventID, managerByID, playerByID, currentResults, currentManagerStats, plotTwist)
	if persistAwards && len(awardRows) > 0 {
		if err := e.store.SaveGameweekAwards(ctx, e.leagueID, eventID, awardRows); err != nil {
			return nil, fmt.Errorf("save awards for event %d: %w", eventID, err)
		}
	}
	if awardsAlert != nil {
		alerts = append(alerts, notify.Alert{
			Kind:           notify.AlertKindGameweekAwards,
			LeagueID:       e.leagueID,
			EventID:        eventID,
			GameweekAwards: awardsAlert,
		})
	}

	for _, result := range currentResults {
		alerts = append(alerts, notify.Alert{
			Kind:     notify.AlertKindH2HResult,
			LeagueID: e.leagueID,
			EventID:  eventID,
			H2HResult: &notify.H2HResultAlert{
				Manager1: managerRef(managerByID, result.Manager1ID),
				Score1:   result.Manager1Score,
				Manager2: managerRef(managerByID, result.Manager2ID),
				Score2:   result.Manager2Score,
				WinnerID: winnerID(result),
			},
		})
	}

	return alerts, nil
}

// GetCurrentStreaks returns all active win/loss streaks as of the latest
// stored event, sorted longest-first.
func (e *Engine) GetCurrentStreaks(ctx context.Context) ([]CurrentStreak, error) {
	latestEventID, err := e.store.GetLatestEventID(ctx, e.leagueID)
	if err != nil {
		return nil, fmt.Errorf("get latest event id: %w", err)
	}
	if latestEventID == 0 {
		return nil, nil
	}

	managerByID, err := e.getManagerDirectory(ctx)
	if err != nil {
		return nil, err
	}

	results, err := e.store.GetH2HResultsRange(ctx, e.leagueID, 1, latestEventID)
	if err != nil {
		return nil, fmt.Errorf("get h2h result history through event %d: %w", latestEventID, err)
	}

	return buildCurrentStreaks(managerByID, results), nil
}

// GetH2HRecord returns the lifetime record between two managers.
func (e *Engine) GetH2HRecord(ctx context.Context, managerAID int64, managerBID int64) (H2HRecord, error) {
	if managerAID == managerBID {
		return H2HRecord{}, fmt.Errorf("manager ids must be distinct")
	}

	managerByID, err := e.getManagerDirectory(ctx)
	if err != nil {
		return H2HRecord{}, err
	}

	managerA, ok := managerByID[managerAID]
	if !ok {
		return H2HRecord{}, fmt.Errorf("manager %d: %w", managerAID, store.ErrNotFound)
	}
	managerB, ok := managerByID[managerBID]
	if !ok {
		return H2HRecord{}, fmt.Errorf("manager %d: %w", managerBID, store.ErrNotFound)
	}

	latestEventID, err := e.store.GetLatestEventID(ctx, e.leagueID)
	if err != nil {
		return H2HRecord{}, fmt.Errorf("get latest event id: %w", err)
	}

	record := H2HRecord{
		LeagueID: e.leagueID,
		ManagerA: managerA,
		ManagerB: managerB,
	}
	if latestEventID == 0 {
		return record, nil
	}

	results, err := e.store.GetH2HResultsRange(ctx, e.leagueID, 1, latestEventID)
	if err != nil {
		return H2HRecord{}, fmt.Errorf("get h2h result history through event %d: %w", latestEventID, err)
	}

	for _, result := range results {
		if !isMatchup(result, managerAID, managerBID) {
			continue
		}

		record.GamesPlayed++

		scoreA, scoreB := scoresForManagers(result, managerAID, managerBID)
		record.ManagerAScore += scoreA
		record.ManagerBScore += scoreB

		switch {
		case scoreA > scoreB:
			record.ManagerAWins++
		case scoreB > scoreA:
			record.ManagerBWins++
		default:
			record.Draws++
		}
	}

	return record, nil
}

func (e *Engine) getManagerDirectory(ctx context.Context) (map[int64]notify.ManagerRef, error) {
	managers, err := e.store.GetManagers(ctx, e.leagueID)
	if err != nil {
		return nil, fmt.Errorf("get managers: %w", err)
	}

	managerByID := make(map[int64]notify.ManagerRef, len(managers))
	for _, manager := range managers {
		managerByID[manager.ID] = notify.ManagerRef{
			ID:       manager.ID,
			Name:     manager.Name,
			TeamName: manager.TeamName,
		}
	}
	return managerByID, nil
}

func (e *Engine) getPlayerDirectory(ctx context.Context) map[int]notify.PlayerRef {
	if e.playerLookup == nil {
		return map[int]notify.PlayerRef{}
	}

	playerByID, err := e.playerLookup.PlayerNames(ctx)
	if err != nil {
		return map[int]notify.PlayerRef{}
	}
	return playerByID
}

func (e *Engine) hasHistoricalStandings(ctx context.Context, eventID int) (bool, error) {
	meta, err := e.store.GetSnapshotMeta(ctx, e.leagueID, eventID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("get snapshot meta for event %d: %w", eventID, err)
	}
	return meta.StandingsFidelity == historicalStandings, nil
}

func (e *Engine) getHistoricalRanks(ctx context.Context, eventID int) (map[int64]int, bool, error) {
	if eventID <= 0 {
		return nil, false, nil
	}

	historical, err := e.hasHistoricalStandings(ctx, eventID)
	if err != nil {
		return nil, false, err
	}
	if !historical {
		return nil, false, nil
	}

	standings, err := e.store.GetStandings(ctx, e.leagueID, eventID)
	if err != nil {
		return nil, false, fmt.Errorf("get standings for event %d: %w", eventID, err)
	}
	return ranksByManager(standings), true, nil
}

func buildCurrentStreaks(managerByID map[int64]notify.ManagerRef, results []store.H2HResult) []CurrentStreak {
	// Normalize result ordering here so streak logic does not depend on any
	// specific Store implementation or fake returning rows chronologically.
	sortedResults := append([]store.H2HResult(nil), results...)
	sort.Slice(sortedResults, func(i, j int) bool {
		if sortedResults[i].EventID != sortedResults[j].EventID {
			return sortedResults[i].EventID < sortedResults[j].EventID
		}
		if sortedResults[i].Manager1ID != sortedResults[j].Manager1ID {
			return sortedResults[i].Manager1ID < sortedResults[j].Manager1ID
		}
		return sortedResults[i].Manager2ID < sortedResults[j].Manager2ID
	})

	outcomes := make(map[int64][]eventOutcome)
	for _, result := range sortedResults {
		manager1Outcome, manager2Outcome := outcomesForResult(result)
		outcomes[result.Manager1ID] = append(outcomes[result.Manager1ID], eventOutcome{
			EventID: result.EventID,
			Kind:    manager1Outcome,
		})
		outcomes[result.Manager2ID] = append(outcomes[result.Manager2ID], eventOutcome{
			EventID: result.EventID,
			Kind:    manager2Outcome,
		})
	}

	streaks := make([]CurrentStreak, 0, len(outcomes))
	for managerID, history := range outcomes {
		if len(history) == 0 {
			continue
		}

		last := history[len(history)-1]
		if last.Kind == "" {
			continue
		}

		length := 1
		startedAt := last.EventID
		for i := len(history) - 2; i >= 0; i-- {
			if history[i].Kind != last.Kind {
				break
			}
			length++
			startedAt = history[i].EventID
		}

		streaks = append(streaks, CurrentStreak{
			Manager:    managerRef(managerByID, managerID),
			Kind:       last.Kind,
			Length:     length,
			StartedAt:  startedAt,
			FinishedAt: last.EventID,
		})
	}

	sort.Slice(streaks, func(i, j int) bool {
		if streaks[i].Length != streaks[j].Length {
			return streaks[i].Length > streaks[j].Length
		}
		if streaks[i].FinishedAt != streaks[j].FinishedAt {
			return streaks[i].FinishedAt > streaks[j].FinishedAt
		}
		if streaks[i].Manager.Name != streaks[j].Manager.Name {
			return streaks[i].Manager.Name < streaks[j].Manager.Name
		}
		return streaks[i].Manager.ID < streaks[j].Manager.ID
	})

	return streaks
}

func buildPlotTwist(managerByID map[int64]notify.ManagerRef, results []store.H2HResult, prevRanks map[int64]int, prevHistorical bool) *notify.UpsetAlert {
	if len(results) == 0 {
		return nil
	}
	if !prevHistorical {
		return nil
	}

	var bestUpset *notify.UpsetAlert
	for _, result := range results {
		winnerID := winnerID(result)
		if winnerID == nil {
			continue
		}

		var (
			loserID     int64
			winnerScore int
			loserScore  int
		)
		if *winnerID == result.Manager1ID {
			loserID = result.Manager2ID
			winnerScore = result.Manager1Score
			loserScore = result.Manager2Score
		} else {
			loserID = result.Manager1ID
			winnerScore = result.Manager2Score
			loserScore = result.Manager1Score
		}

		winnerPrevRank, okWinner := prevRanks[*winnerID]
		loserPrevRank, okLoser := prevRanks[loserID]
		if !okWinner || !okLoser || winnerPrevRank <= loserPrevRank {
			continue
		}

		candidate := &notify.UpsetAlert{
			Winner:             managerRef(managerByID, *winnerID),
			WinnerScore:        winnerScore,
			WinnerPreviousRank: winnerPrevRank,
			Loser:              managerRef(managerByID, loserID),
			LoserScore:         loserScore,
			LoserPreviousRank:  loserPrevRank,
			RankGap:            winnerPrevRank - loserPrevRank,
		}

		if bestUpset == nil ||
			candidate.RankGap > bestUpset.RankGap ||
			(candidate.RankGap == bestUpset.RankGap && (candidate.WinnerScore-candidate.LoserScore) > (bestUpset.WinnerScore-bestUpset.LoserScore)) ||
			(candidate.RankGap == bestUpset.RankGap && (candidate.WinnerScore-candidate.LoserScore) == (bestUpset.WinnerScore-bestUpset.LoserScore) && candidate.Winner.ID < bestUpset.Winner.ID) {
			bestUpset = candidate
		}
	}

	return bestUpset
}

func ranksByManager(standings []store.GameweekStanding) map[int64]int {
	ranks := make(map[int64]int, len(standings))
	for _, standing := range standings {
		ranks[standing.ManagerID] = standing.Rank
	}
	return ranks
}

func managersInResults(results []store.H2HResult) map[int64]struct{} {
	managerIDs := make(map[int64]struct{}, len(results)*2)
	for _, result := range results {
		managerIDs[result.Manager1ID] = struct{}{}
		managerIDs[result.Manager2ID] = struct{}{}
	}
	return managerIDs
}

func managerRef(managerByID map[int64]notify.ManagerRef, managerID int64) notify.ManagerRef {
	if manager, ok := managerByID[managerID]; ok {
		return manager
	}
	return notify.ManagerRef{ID: managerID}
}

func winnerID(result store.H2HResult) *int64 {
	switch {
	case result.Manager1Score > result.Manager2Score:
		winner := result.Manager1ID
		return &winner
	case result.Manager2Score > result.Manager1Score:
		winner := result.Manager2ID
		return &winner
	default:
		return nil
	}
}

func outcomesForResult(result store.H2HResult) (notify.StreakKind, notify.StreakKind) {
	switch {
	case result.Manager1Score > result.Manager2Score:
		return notify.StreakKindWin, notify.StreakKindLoss
	case result.Manager2Score > result.Manager1Score:
		return notify.StreakKindLoss, notify.StreakKindWin
	default:
		return "", ""
	}
}

func isMatchup(result store.H2HResult, managerAID int64, managerBID int64) bool {
	return (result.Manager1ID == managerAID && result.Manager2ID == managerBID) ||
		(result.Manager1ID == managerBID && result.Manager2ID == managerAID)
}

func scoresForManagers(result store.H2HResult, managerAID int64, managerBID int64) (int, int) {
	switch {
	case result.Manager1ID == managerAID && result.Manager2ID == managerBID:
		return result.Manager1Score, result.Manager2Score
	case result.Manager1ID == managerBID && result.Manager2ID == managerAID:
		return result.Manager2Score, result.Manager1Score
	default:
		return 0, 0
	}
}
