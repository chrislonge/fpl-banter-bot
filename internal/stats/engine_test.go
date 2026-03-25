package stats

import (
	"context"
	"testing"

	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

type fakeStore struct {
	standingsByEvent    map[int][]store.GameweekStanding
	chipsByEvent        map[int][]store.ChipUsage
	resultsByEvent      map[int][]store.H2HResult
	managerStatsByEvent map[int][]store.GameweekManagerStat
	savedAwardsByEvent  map[int][]store.GameweekAward
	metaByEvent         map[int]store.SnapshotMeta
	managers            []store.Manager
	latestEventID       int
}

func (f *fakeStore) GetStandings(_ context.Context, _ int64, eventID int) ([]store.GameweekStanding, error) {
	return cloneSlice(f.standingsByEvent[eventID]), nil
}

func (f *fakeStore) GetChipUsage(_ context.Context, _ int64, eventID int) ([]store.ChipUsage, error) {
	return cloneSlice(f.chipsByEvent[eventID]), nil
}

func (f *fakeStore) GetH2HResults(_ context.Context, _ int64, eventID int) ([]store.H2HResult, error) {
	return cloneSlice(f.resultsByEvent[eventID]), nil
}

func (f *fakeStore) GetH2HResultsRange(_ context.Context, _ int64, fromEvent int, toEvent int) ([]store.H2HResult, error) {
	var results []store.H2HResult
	for eventID := fromEvent; eventID <= toEvent; eventID++ {
		results = append(results, f.resultsByEvent[eventID]...)
	}
	return cloneSlice(results), nil
}

func (f *fakeStore) GetGameweekManagerStats(_ context.Context, _ int64, eventID int) ([]store.GameweekManagerStat, error) {
	return cloneSlice(f.managerStatsByEvent[eventID]), nil
}

func (f *fakeStore) GetManagers(_ context.Context, _ int64) ([]store.Manager, error) {
	return cloneSlice(f.managers), nil
}

func (f *fakeStore) GetLatestEventID(_ context.Context, _ int64) (int, error) {
	return f.latestEventID, nil
}

func (f *fakeStore) GetSnapshotMeta(_ context.Context, _ int64, eventID int) (store.SnapshotMeta, error) {
	meta, ok := f.metaByEvent[eventID]
	if !ok {
		return store.SnapshotMeta{}, store.ErrNotFound
	}
	return meta, nil
}

func (f *fakeStore) SaveGameweekAwards(_ context.Context, _ int64, eventID int, awards []store.GameweekAward) error {
	if f.savedAwardsByEvent == nil {
		f.savedAwardsByEvent = make(map[int][]store.GameweekAward)
	}
	f.savedAwardsByEvent[eventID] = cloneSlice(awards)
	return nil
}

func TestBuildGameweekAlertsHistoricalRankChangesAndSummary(t *testing.T) {
	engine := New(&fakeStore{
		standingsByEvent: map[int][]store.GameweekStanding{
			1: {
				{LeagueID: 916670, EventID: 1, ManagerID: 202, Rank: 1},
				{LeagueID: 916670, EventID: 1, ManagerID: 101, Rank: 2},
				{LeagueID: 916670, EventID: 1, ManagerID: 303, Rank: 3},
				{LeagueID: 916670, EventID: 1, ManagerID: 404, Rank: 4},
			},
			2: {
				{LeagueID: 916670, EventID: 2, ManagerID: 101, Rank: 1},
				{LeagueID: 916670, EventID: 2, ManagerID: 202, Rank: 2},
				{LeagueID: 916670, EventID: 2, ManagerID: 404, Rank: 3},
				{LeagueID: 916670, EventID: 2, ManagerID: 303, Rank: 4},
			},
		},
		chipsByEvent: map[int][]store.ChipUsage{
			2: {
				{LeagueID: 916670, ManagerID: 303, EventID: 2, Chip: "wildcard"},
			},
		},
		managerStatsByEvent: map[int][]store.GameweekManagerStat{
			2: {
				{LeagueID: 916670, EventID: 2, ManagerID: 101, PointsOnBench: 3, CaptainElementID: intPtr(430), CaptainPoints: intPtr(13), CaptainMultiplier: intPtr(2)},
				{LeagueID: 916670, EventID: 2, ManagerID: 202, PointsOnBench: 8, CaptainElementID: intPtr(328), CaptainPoints: intPtr(2), CaptainMultiplier: intPtr(2)},
				{LeagueID: 916670, EventID: 2, ManagerID: 303, PointsOnBench: 15, CaptainElementID: intPtr(430), CaptainPoints: intPtr(13), CaptainMultiplier: intPtr(2)},
				{LeagueID: 916670, EventID: 2, ManagerID: 404, PointsOnBench: 6, CaptainElementID: intPtr(430), CaptainPoints: intPtr(13), CaptainMultiplier: intPtr(2)},
			},
		},
		resultsByEvent: map[int][]store.H2HResult{
			1: {
				{LeagueID: 916670, EventID: 1, Manager1ID: 101, Manager1Score: 60, Manager2ID: 303, Manager2Score: 40},
				{LeagueID: 916670, EventID: 1, Manager1ID: 202, Manager1Score: 55, Manager2ID: 404, Manager2Score: 50},
			},
			2: {
				{LeagueID: 916670, EventID: 2, Manager1ID: 101, Manager1Score: 70, Manager2ID: 202, Manager2Score: 60},
				{LeagueID: 916670, EventID: 2, Manager1ID: 303, Manager1Score: 40, Manager2ID: 404, Manager2Score: 55},
			},
		},
		metaByEvent: map[int]store.SnapshotMeta{
			1: {LeagueID: 916670, EventID: 1, StandingsFidelity: "historical"},
			2: {LeagueID: 916670, EventID: 2, StandingsFidelity: "historical"},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
			{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
			{LeagueID: 916670, ID: 303, Name: "Charlie", TeamName: "Charlie FC"},
			{LeagueID: 916670, ID: 404, Name: "Dave", TeamName: "Dave FC"},
		},
		latestEventID: 2,
	}, 916670)

	alerts, err := engine.BuildGameweekAlerts(context.Background(), 2)
	if err != nil {
		t.Fatalf("BuildGameweekAlerts: %v", err)
	}
	if len(alerts) != 9 {
		t.Fatalf("len(alerts) = %d, want 9", len(alerts))
	}

	rankChanges := alertsByKind(alerts, notify.AlertKindRankChange)
	if len(rankChanges) != 4 {
		t.Fatalf("len(rankChanges) = %d, want 4", len(rankChanges))
	}
	aliceRankChange := findRankChange(t, rankChanges, 101)
	if aliceRankChange.CurrentRank != 1 || aliceRankChange.PreviousRank != 2 || !aliceRankChange.MovedIntoFirst {
		t.Errorf("alice rank change = %+v, want 2 -> 1 movedIntoFirst=true", aliceRankChange)
	}

	chipAlerts := alertsByKind(alerts, notify.AlertKindChipUsage)
	if len(chipAlerts) != 1 {
		t.Fatalf("len(chipAlerts) = %d, want 1", len(chipAlerts))
	}
	if chipAlerts[0].ChipUsage.Manager.ID != 303 || chipAlerts[0].ChipUsage.Chip != "wildcard" {
		t.Errorf("chip alert = %+v, want Charlie wildcard", chipAlerts[0].ChipUsage)
	}

	summaryAlerts := alertsByKind(alerts, notify.AlertKindGameweekSummary)
	if len(summaryAlerts) != 1 {
		t.Fatalf("len(summaryAlerts) = %d, want 1", len(summaryAlerts))
	}
	summary := summaryAlerts[0].GameweekSummary
	if summary.HighScorer.Manager.ID != 101 || summary.HighScorer.Score != 70 {
		t.Errorf("high scorer = %+v, want Alice 70", summary.HighScorer)
	}
	if summary.LowScorer.Manager.ID != 303 || summary.LowScorer.Score != 40 {
		t.Errorf("low scorer = %+v, want Charlie 40", summary.LowScorer)
	}
	if summary.BiggestUpset == nil {
		t.Fatal("summary.BiggestUpset = nil, want Dave over Charlie")
	}
	if summary.BiggestUpset.Winner.ID != 404 || summary.BiggestUpset.Loser.ID != 303 {
		t.Errorf("biggest upset = %+v, want Dave over Charlie", summary.BiggestUpset)
	}

	awardAlerts := alertsByKind(alerts, notify.AlertKindGameweekAwards)
	if len(awardAlerts) != 1 {
		t.Fatalf("len(awardAlerts) = %d, want 1", len(awardAlerts))
	}
	if awardAlerts[0].GameweekAwards == nil || awardAlerts[0].GameweekAwards.BenchWarmer == nil {
		t.Fatalf("expected bench warmer award in %+v", awardAlerts[0].GameweekAwards)
	}
	if awardAlerts[0].GameweekAwards.BenchWarmer.Manager.ID != 303 {
		t.Errorf("bench warmer winner = %d, want 303", awardAlerts[0].GameweekAwards.BenchWarmer.Manager.ID)
	}
	if awardAlerts[0].GameweekAwards.ArmbandOfShame == nil || awardAlerts[0].GameweekAwards.ArmbandOfShame.Manager.ID != 202 {
		t.Errorf("armband of shame = %+v, want Bob", awardAlerts[0].GameweekAwards.ArmbandOfShame)
	}
	if got := len(engine.store.(*fakeStore).savedAwardsByEvent[2]); got != 8 {
		t.Errorf("saved awards count = %d, want 8", got)
	}

	h2hAlerts := alertsByKind(alerts, notify.AlertKindH2HResult)
	if len(h2hAlerts) != 2 {
		t.Fatalf("len(h2hAlerts) = %d, want 2", len(h2hAlerts))
	}
}

func TestBuildGameweekAlertsSyntheticStandingsStillAllowStreaksAndChips(t *testing.T) {
	engine := New(&fakeStore{
		standingsByEvent: map[int][]store.GameweekStanding{
			1: {
				{LeagueID: 916670, EventID: 1, ManagerID: 101, Rank: 3},
				{LeagueID: 916670, EventID: 1, ManagerID: 202, Rank: 1},
				{LeagueID: 916670, EventID: 1, ManagerID: 303, Rank: 2},
				{LeagueID: 916670, EventID: 1, ManagerID: 404, Rank: 4},
			},
			2: {
				{LeagueID: 916670, EventID: 2, ManagerID: 101, Rank: 2},
				{LeagueID: 916670, EventID: 2, ManagerID: 202, Rank: 3},
				{LeagueID: 916670, EventID: 2, ManagerID: 303, Rank: 1},
				{LeagueID: 916670, EventID: 2, ManagerID: 404, Rank: 4},
			},
			3: {
				{LeagueID: 916670, EventID: 3, ManagerID: 101, Rank: 1},
				{LeagueID: 916670, EventID: 3, ManagerID: 303, Rank: 2},
				{LeagueID: 916670, EventID: 3, ManagerID: 202, Rank: 3},
				{LeagueID: 916670, EventID: 3, ManagerID: 404, Rank: 4},
			},
		},
		chipsByEvent: map[int][]store.ChipUsage{
			3: {
				{LeagueID: 916670, ManagerID: 202, EventID: 3, Chip: "3xc"},
			},
		},
		managerStatsByEvent: map[int][]store.GameweekManagerStat{
			3: {
				{LeagueID: 916670, EventID: 3, ManagerID: 101, PointsOnBench: 4, CaptainElementID: intPtr(430), CaptainPoints: intPtr(12), CaptainMultiplier: intPtr(2)},
				{LeagueID: 916670, EventID: 3, ManagerID: 404, PointsOnBench: 11, CaptainElementID: intPtr(328), CaptainPoints: intPtr(1), CaptainMultiplier: intPtr(2)},
			},
		},
		resultsByEvent: map[int][]store.H2HResult{
			1: {
				{LeagueID: 916670, EventID: 1, Manager1ID: 101, Manager1Score: 61, Manager2ID: 202, Manager2Score: 50},
			},
			2: {
				{LeagueID: 916670, EventID: 2, Manager1ID: 101, Manager1Score: 66, Manager2ID: 303, Manager2Score: 58},
			},
			3: {
				{LeagueID: 916670, EventID: 3, Manager1ID: 101, Manager1Score: 72, Manager2ID: 404, Manager2Score: 49},
			},
		},
		metaByEvent: map[int]store.SnapshotMeta{
			1: {LeagueID: 916670, EventID: 1, StandingsFidelity: "historical"},
			2: {LeagueID: 916670, EventID: 2, StandingsFidelity: "historical"},
			3: {LeagueID: 916670, EventID: 3, StandingsFidelity: "synthetic"},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
			{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
			{LeagueID: 916670, ID: 303, Name: "Charlie", TeamName: "Charlie FC"},
			{LeagueID: 916670, ID: 404, Name: "Dave", TeamName: "Dave FC"},
		},
		latestEventID: 3,
	}, 916670)

	alerts, err := engine.BuildGameweekAlerts(context.Background(), 3)
	if err != nil {
		t.Fatalf("BuildGameweekAlerts: %v", err)
	}

	if got := len(alertsByKind(alerts, notify.AlertKindRankChange)); got != 0 {
		t.Fatalf("len(rankChanges) = %d, want 0", got)
	}

	streakAlerts := alertsByKind(alerts, notify.AlertKindStreak)
	if len(streakAlerts) != 1 {
		t.Fatalf("len(streakAlerts) = %d, want 1", len(streakAlerts))
	}
	if streakAlerts[0].Streak.Manager.ID != 101 || streakAlerts[0].Streak.Kind != notify.StreakKindWin || streakAlerts[0].Streak.Length != 3 {
		t.Errorf("streak alert = %+v, want Alice win streak of 3", streakAlerts[0].Streak)
	}

	chipAlerts := alertsByKind(alerts, notify.AlertKindChipUsage)
	if len(chipAlerts) != 1 || chipAlerts[0].ChipUsage.Manager.ID != 202 {
		t.Errorf("chip alerts = %+v, want Bob chip alert", chipAlerts)
	}

	if got := len(alertsByKind(alerts, notify.AlertKindGameweekSummary)); got != 1 {
		t.Fatalf("len(summaryAlerts) = %d, want 1", got)
	}
	if got := len(alertsByKind(alerts, notify.AlertKindGameweekAwards)); got != 1 {
		t.Fatalf("len(awardAlerts) = %d, want 1", got)
	}
	if got := len(alertsByKind(alerts, notify.AlertKindH2HResult)); got != 1 {
		t.Fatalf("len(h2hAlerts) = %d, want 1", got)
	}
}

func TestBuildGameweekAlertsReadOnly_DoesNotPersistAwards(t *testing.T) {
	fs := &fakeStore{
		standingsByEvent: map[int][]store.GameweekStanding{
			2: {
				{LeagueID: 916670, EventID: 2, ManagerID: 101, Rank: 1},
				{LeagueID: 916670, EventID: 2, ManagerID: 202, Rank: 2},
			},
		},
		managerStatsByEvent: map[int][]store.GameweekManagerStat{
			2: {
				{LeagueID: 916670, EventID: 2, ManagerID: 101, PointsOnBench: 4, CaptainElementID: intPtr(430), CaptainPoints: intPtr(13), CaptainMultiplier: intPtr(2)},
				{LeagueID: 916670, EventID: 2, ManagerID: 202, PointsOnBench: 2, CaptainElementID: intPtr(328), CaptainPoints: intPtr(3), CaptainMultiplier: intPtr(2)},
			},
		},
		resultsByEvent: map[int][]store.H2HResult{
			1: {
				{LeagueID: 916670, EventID: 1, Manager1ID: 101, Manager1Score: 55, Manager2ID: 202, Manager2Score: 45},
			},
			2: {
				{LeagueID: 916670, EventID: 2, Manager1ID: 101, Manager1Score: 70, Manager2ID: 202, Manager2Score: 60},
			},
		},
		metaByEvent: map[int]store.SnapshotMeta{
			1: {LeagueID: 916670, EventID: 1, StandingsFidelity: "historical"},
			2: {LeagueID: 916670, EventID: 2, StandingsFidelity: "historical"},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
			{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
		},
	}
	engine := New(fs, 916670)

	alerts, err := engine.BuildGameweekAlertsReadOnly(context.Background(), 2)
	if err != nil {
		t.Fatalf("BuildGameweekAlertsReadOnly: %v", err)
	}
	if len(alertsByKind(alerts, notify.AlertKindGameweekAwards)) != 1 {
		t.Fatalf("expected a gameweek awards alert in %+v", alerts)
	}
	if fs.savedAwardsByEvent != nil {
		t.Fatalf("expected no persisted awards in read-only mode, got %+v", fs.savedAwardsByEvent)
	}
}

func TestBuildGameweekAlerts_NoResultsDoesNotPersistEmptyAwards(t *testing.T) {
	fs := &fakeStore{
		standingsByEvent: map[int][]store.GameweekStanding{
			2: {
				{LeagueID: 916670, EventID: 2, ManagerID: 101, Rank: 1},
				{LeagueID: 916670, EventID: 2, ManagerID: 202, Rank: 2},
			},
		},
		metaByEvent: map[int]store.SnapshotMeta{
			2: {LeagueID: 916670, EventID: 2, StandingsFidelity: "historical"},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
			{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
		},
	}
	engine := New(fs, 916670)

	alerts, err := engine.BuildGameweekAlerts(context.Background(), 2)
	if err != nil {
		t.Fatalf("BuildGameweekAlerts: %v", err)
	}
	if len(alertsByKind(alerts, notify.AlertKindGameweekAwards)) != 0 {
		t.Fatalf("expected no awards alert when results are missing, got %+v", alerts)
	}
	if fs.savedAwardsByEvent != nil {
		t.Fatalf("expected no persisted awards when no rows were computed, got %+v", fs.savedAwardsByEvent)
	}
}

func TestGetCurrentStreaks(t *testing.T) {
	engine := New(&fakeStore{
		resultsByEvent: map[int][]store.H2HResult{
			1: {
				{LeagueID: 916670, EventID: 1, Manager1ID: 101, Manager1Score: 61, Manager2ID: 202, Manager2Score: 50},
				{LeagueID: 916670, EventID: 1, Manager1ID: 303, Manager1Score: 40, Manager2ID: 404, Manager2Score: 52},
			},
			2: {
				{LeagueID: 916670, EventID: 2, Manager1ID: 101, Manager1Score: 66, Manager2ID: 303, Manager2Score: 58},
				{LeagueID: 916670, EventID: 2, Manager1ID: 202, Manager1Score: 38, Manager2ID: 404, Manager2Score: 59},
			},
			3: {
				{LeagueID: 916670, EventID: 3, Manager1ID: 101, Manager1Score: 72, Manager2ID: 404, Manager2Score: 49},
				{LeagueID: 916670, EventID: 3, Manager1ID: 202, Manager1Score: 44, Manager2ID: 303, Manager2Score: 55},
			},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
			{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
			{LeagueID: 916670, ID: 303, Name: "Charlie", TeamName: "Charlie FC"},
			{LeagueID: 916670, ID: 404, Name: "Dave", TeamName: "Dave FC"},
		},
		latestEventID: 3,
	}, 916670)

	streaks, err := engine.GetCurrentStreaks(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentStreaks: %v", err)
	}
	if len(streaks) != 4 {
		t.Fatalf("len(streaks) = %d, want 4", len(streaks))
	}
	if streaks[0].Manager.ID != 101 || streaks[0].Kind != notify.StreakKindWin || streaks[0].Length != 3 {
		t.Errorf("streaks[0] = %+v, want Alice win streak of 3", streaks[0])
	}
	if streaks[1].Manager.ID != 202 || streaks[1].Kind != notify.StreakKindLoss || streaks[1].Length != 3 {
		t.Errorf("streaks[1] = %+v, want Bob loss streak of 3", streaks[1])
	}
	if streaks[2].Manager.ID != 303 || streaks[2].Kind != notify.StreakKindWin || streaks[2].Length != 1 {
		t.Errorf("streaks[2] = %+v, want Charlie win streak of 1", streaks[2])
	}
	if streaks[3].Manager.ID != 404 || streaks[3].Kind != notify.StreakKindLoss || streaks[3].Length != 1 {
		t.Errorf("streaks[3] = %+v, want Dave loss streak of 1", streaks[3])
	}
}

func TestBuildCurrentStreaksSortsResultsBeforeComputingCurrentStreak(t *testing.T) {
	managerByID := map[int64]notify.ManagerRef{
		101: {ID: 101, Name: "Alice", TeamName: "Alice FC"},
		202: {ID: 202, Name: "Bob", TeamName: "Bob FC"},
	}

	streaks := buildCurrentStreaks(managerByID, []store.H2HResult{
		{LeagueID: 916670, EventID: 1, Manager1ID: 101, Manager1Score: 40, Manager2ID: 202, Manager2Score: 60},
		{LeagueID: 916670, EventID: 3, Manager1ID: 101, Manager1Score: 72, Manager2ID: 202, Manager2Score: 49},
		{LeagueID: 916670, EventID: 2, Manager1ID: 101, Manager1Score: 66, Manager2ID: 202, Manager2Score: 58},
	})

	if len(streaks) != 2 {
		t.Fatalf("len(streaks) = %d, want 2", len(streaks))
	}

	if streaks[0].Manager.ID != 101 || streaks[0].Kind != notify.StreakKindWin || streaks[0].Length != 2 || streaks[0].StartedAt != 2 || streaks[0].FinishedAt != 3 {
		t.Errorf("streaks[0] = %+v, want Alice win streak from GW2-GW3", streaks[0])
	}
	if streaks[1].Manager.ID != 202 || streaks[1].Kind != notify.StreakKindLoss || streaks[1].Length != 2 || streaks[1].StartedAt != 2 || streaks[1].FinishedAt != 3 {
		t.Errorf("streaks[1] = %+v, want Bob loss streak from GW2-GW3", streaks[1])
	}
}

func TestGetH2HRecord(t *testing.T) {
	engine := New(&fakeStore{
		resultsByEvent: map[int][]store.H2HResult{
			1: {
				{LeagueID: 916670, EventID: 1, Manager1ID: 101, Manager1Score: 60, Manager2ID: 202, Manager2Score: 50},
			},
			2: {
				{LeagueID: 916670, EventID: 2, Manager1ID: 101, Manager1Score: 40, Manager2ID: 202, Manager2Score: 70},
			},
			4: {
				{LeagueID: 916670, EventID: 4, Manager1ID: 101, Manager1Score: 55, Manager2ID: 202, Manager2Score: 55},
			},
		},
		managers: []store.Manager{
			{LeagueID: 916670, ID: 101, Name: "Alice", TeamName: "Alice FC"},
			{LeagueID: 916670, ID: 202, Name: "Bob", TeamName: "Bob FC"},
		},
		latestEventID: 4,
	}, 916670)

	record, err := engine.GetH2HRecord(context.Background(), 101, 202)
	if err != nil {
		t.Fatalf("GetH2HRecord: %v", err)
	}
	if record.GamesPlayed != 3 {
		t.Fatalf("record.GamesPlayed = %d, want 3", record.GamesPlayed)
	}
	if record.ManagerAWins != 1 || record.ManagerBWins != 1 || record.Draws != 1 {
		t.Errorf("record wins/draws = %+v, want A=1 B=1 D=1", record)
	}
	if record.ManagerAScore != 155 || record.ManagerBScore != 175 {
		t.Errorf("record scores = A:%d B:%d, want 155/175", record.ManagerAScore, record.ManagerBScore)
	}
}

func alertsByKind(alerts []notify.Alert, kind notify.AlertKind) []notify.Alert {
	var filtered []notify.Alert
	for _, alert := range alerts {
		if alert.Kind == kind {
			filtered = append(filtered, alert)
		}
	}
	return filtered
}

func findRankChange(t *testing.T, alerts []notify.Alert, managerID int64) notify.RankChangeAlert {
	t.Helper()
	for _, alert := range alerts {
		if alert.RankChange != nil && alert.RankChange.Manager.ID == managerID {
			return *alert.RankChange
		}
	}
	t.Fatalf("no rank change alert for manager %d", managerID)
	return notify.RankChangeAlert{}
}

func cloneSlice[T any](in []T) []T {
	if len(in) == 0 {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

func intPtr(v int) *int {
	return &v
}
