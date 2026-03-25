// ---------------------------------------------------------------------------
// Go concept — BLACK-BOX TESTING (package store_test):
//
// Like the FPL client tests, this file uses `store_test` (not `store`)
// as the package name. Tests can only access exported symbols, which
// validates the public API as callers will actually use it.
//
// Go concept — INTEGRATION TESTS GATED BY ENV VAR:
//
// These tests require a running Postgres instance. Rather than mocking
// the database (which would miss real SQL bugs), we test against the
// real thing. The tradeoff: these tests are skipped when
// STORE_TEST_DATABASE_URL is not set, so `go test ./...` still passes
// on any machine without a database.
//
// TestMain is a special function — it runs BEFORE any test in the
// package. We use it for one-time setup (create pool, run migrations)
// and teardown (close pool). If the env var is missing, we call
// os.Exit(0) to skip all tests with a clean exit.
// ---------------------------------------------------------------------------
package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Package-level variables shared by all tests. Set once in TestMain.
var (
	testPool  *pgxpool.Pool
	testStore *store.PostgresStore
)

func TestMain(m *testing.M) {
	dbURL := os.Getenv("STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		fmt.Println("STORE_TEST_DATABASE_URL not set — skipping store integration tests")
		os.Exit(0)
	}

	ctx := context.Background()

	var err error
	testPool, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create test pool: %v\n", err)
		os.Exit(1)
	}

	// Run migrations against the test database so tables exist.
	if err := store.RunMigrations(dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	testStore = store.New(testPool)

	code := m.Run()
	testPool.Close()
	os.Exit(code)
}

// truncateTables removes all data between tests so each test starts
// with a clean slate. TRUNCATE ... CASCADE handles foreign key ordering.
//
// Go concept — t.Helper():
//
// Marking this function with t.Helper() means that if it fails, the
// test output points to the LINE IN THE CALLING TEST, not to the line
// inside truncateTables. Without it, every failure would show this
// function's line number, making it hard to find which test failed.
func truncateTables(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		"TRUNCATE leagues, managers, gameweek_standings, chip_usage, h2h_results, gameweek_snapshot_meta, gameweek_manager_stats, gw_awards CASCADE",
	)
	if err != nil {
		t.Fatalf("failed to truncate tables: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helper: seed a league and managers for tests that need FK parents
// ---------------------------------------------------------------------------

func seedLeague(t *testing.T, id int64, name, leagueType string) {
	t.Helper()
	err := testStore.UpsertLeague(context.Background(), store.League{
		ID:   id,
		Name: name,
		Type: leagueType,
	})
	if err != nil {
		t.Fatalf("failed to seed league: %v", err)
	}
}

func seedManager(t *testing.T, leagueID, id int64, name, teamName string) {
	t.Helper()
	err := testStore.UpsertManager(context.Background(), store.Manager{
		LeagueID: leagueID,
		ID:       id,
		Name:     name,
		TeamName: teamName,
	})
	if err != nil {
		t.Fatalf("failed to seed manager: %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestUpsertAndGetLeague(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	// Insert.
	league := store.League{ID: 100, Name: "Test League", Type: "h2h"}
	if err := testStore.UpsertLeague(ctx, league); err != nil {
		t.Fatalf("UpsertLeague: %v", err)
	}

	// Read back.
	got, err := testStore.GetLeague(ctx, 100)
	if err != nil {
		t.Fatalf("GetLeague: %v", err)
	}
	if got.ID != 100 || got.Name != "Test League" || got.Type != "h2h" {
		t.Errorf("GetLeague = %+v, want ID=100 Name=Test League Type=h2h", got)
	}

	// Upsert with new name — should update, not duplicate.
	league.Name = "Renamed League"
	if err := testStore.UpsertLeague(ctx, league); err != nil {
		t.Fatalf("UpsertLeague (update): %v", err)
	}
	got, err = testStore.GetLeague(ctx, 100)
	if err != nil {
		t.Fatalf("GetLeague after update: %v", err)
	}
	if got.Name != "Renamed League" {
		t.Errorf("Name after upsert = %q, want %q", got.Name, "Renamed League")
	}
}

func TestUpsertAndGetManager(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	// League must exist first (FK constraint).
	seedLeague(t, 100, "Test League", "h2h")

	manager := store.Manager{LeagueID: 100, ID: 1001, Name: "Alice", TeamName: "Alice FC"}
	if err := testStore.UpsertManager(ctx, manager); err != nil {
		t.Fatalf("UpsertManager: %v", err)
	}

	managers, err := testStore.GetManagers(ctx, 100)
	if err != nil {
		t.Fatalf("GetManagers: %v", err)
	}
	if len(managers) != 1 {
		t.Fatalf("len(managers) = %d, want 1", len(managers))
	}
	if managers[0].Name != "Alice" || managers[0].TeamName != "Alice FC" {
		t.Errorf("manager = %+v, want Name=Alice TeamName=Alice FC", managers[0])
	}

	// Upsert with new team name.
	manager.TeamName = "Alice United"
	if err := testStore.UpsertManager(ctx, manager); err != nil {
		t.Fatalf("UpsertManager (update): %v", err)
	}
	managers, err = testStore.GetManagers(ctx, 100)
	if err != nil {
		t.Fatalf("GetManagers after update: %v", err)
	}
	if managers[0].TeamName != "Alice United" {
		t.Errorf("TeamName after upsert = %q, want %q", managers[0].TeamName, "Alice United")
	}
}

func TestUpsertAndGetGameweekStanding(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	standing := store.GameweekStanding{
		LeagueID: 100, EventID: 5, ManagerID: 1001,
		Rank: 1, Points: 30, TotalScore: 350,
	}
	if err := testStore.UpsertGameweekStanding(ctx, standing); err != nil {
		t.Fatalf("UpsertGameweekStanding: %v", err)
	}

	standings, err := testStore.GetStandings(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetStandings: %v", err)
	}
	if len(standings) != 1 {
		t.Fatalf("len(standings) = %d, want 1", len(standings))
	}
	if standings[0].Rank != 1 || standings[0].Points != 30 {
		t.Errorf("standing = %+v, want Rank=1 Points=30", standings[0])
	}

	// Upsert with different rank — should update.
	standing.Rank = 2
	if err := testStore.UpsertGameweekStanding(ctx, standing); err != nil {
		t.Fatalf("UpsertGameweekStanding (update): %v", err)
	}
	standings, err = testStore.GetStandings(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetStandings after update: %v", err)
	}
	if standings[0].Rank != 2 {
		t.Errorf("Rank after upsert = %d, want 2", standings[0].Rank)
	}
}

func TestUpsertChipUsage(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	chip := store.ChipUsage{LeagueID: 100, ManagerID: 1001, EventID: 5, Chip: "wildcard"}
	if err := testStore.UpsertChipUsage(ctx, chip); err != nil {
		t.Fatalf("UpsertChipUsage: %v", err)
	}

	chips, err := testStore.GetChipUsage(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetChipUsage: %v", err)
	}
	if len(chips) != 1 || chips[0].Chip != "wildcard" {
		t.Errorf("chips = %+v, want [{...Chip:wildcard}]", chips)
	}

	// Duplicate insert — DO NOTHING, still one row.
	if err := testStore.UpsertChipUsage(ctx, chip); err != nil {
		t.Fatalf("UpsertChipUsage (duplicate): %v", err)
	}
	chips, err = testStore.GetChipUsage(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetChipUsage after duplicate: %v", err)
	}
	if len(chips) != 1 {
		t.Errorf("len(chips) after duplicate = %d, want 1", len(chips))
	}
}

func TestUpsertAndGetH2HResult(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 100, 1002, "Bob", "Bob FC")

	// manager_1_id must be < manager_2_id (CHECK constraint).
	result := store.H2HResult{
		LeagueID: 100, EventID: 5,
		Manager1ID: 1001, Manager1Score: 65,
		Manager2ID: 1002, Manager2Score: 48,
	}
	if err := testStore.UpsertH2HResult(ctx, result); err != nil {
		t.Fatalf("UpsertH2HResult: %v", err)
	}

	results, err := testStore.GetH2HResults(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetH2HResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Manager1Score != 65 || results[0].Manager2Score != 48 {
		t.Errorf("result = %+v, want scores 65/48", results[0])
	}

	// Upsert with updated scores.
	result.Manager1Score = 70
	if err := testStore.UpsertH2HResult(ctx, result); err != nil {
		t.Fatalf("UpsertH2HResult (update): %v", err)
	}
	results, err = testStore.GetH2HResults(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetH2HResults after update: %v", err)
	}
	if results[0].Manager1Score != 70 {
		t.Errorf("Manager1Score after upsert = %d, want 70", results[0].Manager1Score)
	}
}

func TestGetH2HResultsRange(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 100, 1002, "Bob", "Bob FC")
	seedManager(t, 100, 1003, "Charlie", "Charlie FC")

	for _, result := range []store.H2HResult{
		{LeagueID: 100, EventID: 4, Manager1ID: 1001, Manager1Score: 51, Manager2ID: 1002, Manager2Score: 40},
		{LeagueID: 100, EventID: 6, Manager1ID: 1001, Manager1Score: 62, Manager2ID: 1003, Manager2Score: 58},
		{LeagueID: 100, EventID: 5, Manager1ID: 1002, Manager1Score: 47, Manager2ID: 1003, Manager2Score: 47},
	} {
		if err := testStore.UpsertH2HResult(ctx, result); err != nil {
			t.Fatalf("UpsertH2HResult: %v", err)
		}
	}

	results, err := testStore.GetH2HResultsRange(ctx, 100, 4, 5)
	if err != nil {
		t.Fatalf("GetH2HResultsRange: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].EventID != 4 || results[1].EventID != 5 {
		t.Errorf("event ordering = [%d %d], want [4 5]", results[0].EventID, results[1].EventID)
	}
	if results[0].Manager1ID != 1001 || results[0].Manager2ID != 1002 {
		t.Errorf("first result = %+v, want Alice vs Bob in GW4", results[0])
	}
	if results[1].Manager1ID != 1002 || results[1].Manager2ID != 1003 {
		t.Errorf("second result = %+v, want Bob vs Charlie in GW5", results[1])
	}
}

func TestUpsertAndGetGameweekManagerStat(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	stat := store.GameweekManagerStat{
		LeagueID:          100,
		EventID:           5,
		ManagerID:         1001,
		PointsOnBench:     12,
		CaptainElementID:  intPtr(430),
		CaptainPoints:     intPtr(13),
		CaptainMultiplier: intPtr(2),
	}
	if err := testStore.UpsertGameweekManagerStat(ctx, stat); err != nil {
		t.Fatalf("UpsertGameweekManagerStat: %v", err)
	}

	stats, err := testStore.GetGameweekManagerStats(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetGameweekManagerStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("len(stats) = %d, want 1", len(stats))
	}
	if stats[0].PointsOnBench != 12 {
		t.Errorf("PointsOnBench = %d, want 12", stats[0].PointsOnBench)
	}
	if stats[0].CaptainElementID == nil || *stats[0].CaptainElementID != 430 {
		t.Errorf("CaptainElementID = %v, want 430", stats[0].CaptainElementID)
	}

	stat.PointsOnBench = 8
	stat.CaptainPoints = intPtr(7)
	if err := testStore.UpsertGameweekManagerStat(ctx, stat); err != nil {
		t.Fatalf("UpsertGameweekManagerStat (update): %v", err)
	}

	stats, err = testStore.GetGameweekManagerStats(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetGameweekManagerStats after update: %v", err)
	}
	if stats[0].PointsOnBench != 8 {
		t.Errorf("PointsOnBench after update = %d, want 8", stats[0].PointsOnBench)
	}
	if stats[0].CaptainPoints == nil || *stats[0].CaptainPoints != 7 {
		t.Errorf("CaptainPoints after update = %v, want 7", stats[0].CaptainPoints)
	}
}

func TestUpsertAndGetGameweekAward(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 100, 1002, "Bob", "Bob FC")

	award := store.GameweekAward{
		LeagueID:          100,
		EventID:           5,
		AwardKey:          "biggest_thrashing",
		ManagerID:         1001,
		OpponentManagerID: int64Ptr(1002),
		PlayerElementID:   nil,
		MetricValue:       22,
	}
	if err := testStore.UpsertGameweekAward(ctx, award); err != nil {
		t.Fatalf("UpsertGameweekAward: %v", err)
	}

	awards, err := testStore.GetGameweekAwards(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetGameweekAwards: %v", err)
	}
	if len(awards) != 1 {
		t.Fatalf("len(awards) = %d, want 1", len(awards))
	}
	if awards[0].AwardKey != "biggest_thrashing" || awards[0].MetricValue != 22 {
		t.Errorf("award = %+v, want biggest_thrashing/22", awards[0])
	}

	award.ManagerID = 1002
	award.MetricValue = 3
	if err := testStore.UpsertGameweekAward(ctx, award); err != nil {
		t.Fatalf("UpsertGameweekAward (update): %v", err)
	}

	awards, err = testStore.GetGameweekAwards(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetGameweekAwards after update: %v", err)
	}
	if awards[0].ManagerID != 1002 || awards[0].MetricValue != 3 {
		t.Errorf("award after update = %+v, want manager 1002 metric 3", awards[0])
	}
}

func TestSaveGameweekSnapshot(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 100, 1002, "Bob", "Bob FC")
	seedManager(t, 100, 1003, "Charlie", "Charlie FC")

	standings := []store.GameweekStanding{
		{LeagueID: 100, EventID: 5, ManagerID: 1001, Rank: 1, Points: 30, TotalScore: 350},
		{LeagueID: 100, EventID: 5, ManagerID: 1002, Rank: 2, Points: 24, TotalScore: 310},
		{LeagueID: 100, EventID: 5, ManagerID: 1003, Rank: 3, Points: 18, TotalScore: 280},
	}
	chips := []store.ChipUsage{
		{LeagueID: 100, ManagerID: 1001, EventID: 5, Chip: "wildcard"},
	}
	results := []store.H2HResult{
		{LeagueID: 100, EventID: 5, Manager1ID: 1001, Manager1Score: 65, Manager2ID: 1002, Manager2Score: 48},
	}
	managerStats := []store.GameweekManagerStat{
		{
			LeagueID:          100,
			EventID:           5,
			ManagerID:         1001,
			PointsOnBench:     9,
			CaptainElementID:  intPtr(430),
			CaptainPoints:     intPtr(13),
			CaptainMultiplier: intPtr(2),
		},
	}

	snap := store.GameweekSnapshot{
		Standings: standings,
		Chips:     chips,
		Results:   results,
		ManagerStats: managerStats,
		Meta: store.SnapshotMeta{
		LeagueID: 100, EventID: 5,
		Source: "live", StandingsFidelity: "historical",
		},
	}
	if err := testStore.SaveGameweekSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveGameweekSnapshot: %v", err)
	}

	// Verify all data was written.
	gotStandings, err := testStore.GetStandings(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetStandings: %v", err)
	}
	if len(gotStandings) != 3 {
		t.Errorf("len(standings) = %d, want 3", len(gotStandings))
	}

	gotChips, err := testStore.GetChipUsage(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetChipUsage: %v", err)
	}
	if len(gotChips) != 1 {
		t.Errorf("len(chips) = %d, want 1", len(gotChips))
	}

	gotResults, err := testStore.GetH2HResults(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetH2HResults: %v", err)
	}
	if len(gotResults) != 1 {
		t.Errorf("len(results) = %d, want 1", len(gotResults))
	}

	gotStats, err := testStore.GetGameweekManagerStats(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetGameweekManagerStats: %v", err)
	}
	if len(gotStats) != 1 || gotStats[0].PointsOnBench != 9 {
		t.Errorf("manager stats = %+v, want one row with bench 9", gotStats)
	}

	// Verify metadata was written atomically with the snapshot.
	gotMeta, err := testStore.GetSnapshotMeta(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetSnapshotMeta: %v", err)
	}
	if gotMeta.Source != "live" || gotMeta.StandingsFidelity != "historical" {
		t.Errorf("meta source=%q fidelity=%q, want live/historical", gotMeta.Source, gotMeta.StandingsFidelity)
	}
}

func TestSaveGameweekSnapshotIdempotent(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 100, 1002, "Bob", "Bob FC")

	standings := []store.GameweekStanding{
		{LeagueID: 100, EventID: 5, ManagerID: 1001, Rank: 1, Points: 30, TotalScore: 350},
	}
	chips := []store.ChipUsage{
		{LeagueID: 100, ManagerID: 1001, EventID: 5, Chip: "bboost"},
	}
	results := []store.H2HResult{
		{LeagueID: 100, EventID: 5, Manager1ID: 1001, Manager1Score: 65, Manager2ID: 1002, Manager2Score: 48},
	}

	snap := store.GameweekSnapshot{
		Standings: standings,
		Chips:     chips,
		Results:   results,
		Meta: store.SnapshotMeta{
			LeagueID: 100, EventID: 5,
			Source: "live", StandingsFidelity: "historical",
		},
	}

	// Save twice — should succeed both times with no duplicates.
	if err := testStore.SaveGameweekSnapshot(ctx, snap); err != nil {
		t.Fatalf("first SaveGameweekSnapshot: %v", err)
	}
	if err := testStore.SaveGameweekSnapshot(ctx, snap); err != nil {
		t.Fatalf("second SaveGameweekSnapshot: %v", err)
	}

	gotStandings, err := testStore.GetStandings(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetStandings: %v", err)
	}
	if len(gotStandings) != 1 {
		t.Errorf("len(standings) = %d, want 1", len(gotStandings))
	}
	gotChips, err := testStore.GetChipUsage(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetChipUsage: %v", err)
	}
	if len(gotChips) != 1 {
		t.Errorf("len(chips) = %d, want 1", len(gotChips))
	}
}

func TestGetStandingsOrdering(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 100, 1002, "Bob", "Bob FC")
	seedManager(t, 100, 1003, "Charlie", "Charlie FC")

	// Insert out of rank order.
	for _, st := range []store.GameweekStanding{
		{LeagueID: 100, EventID: 5, ManagerID: 1003, Rank: 3, Points: 18, TotalScore: 280},
		{LeagueID: 100, EventID: 5, ManagerID: 1001, Rank: 1, Points: 30, TotalScore: 350},
		{LeagueID: 100, EventID: 5, ManagerID: 1002, Rank: 2, Points: 24, TotalScore: 310},
	} {
		if err := testStore.UpsertGameweekStanding(ctx, st); err != nil {
			t.Fatalf("UpsertGameweekStanding: %v", err)
		}
	}

	standings, err := testStore.GetStandings(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetStandings: %v", err)
	}

	// Should come back ordered by rank.
	if len(standings) != 3 {
		t.Fatalf("len(standings) = %d, want 3", len(standings))
	}
	for i, want := range []int{1, 2, 3} {
		if standings[i].Rank != want {
			t.Errorf("standings[%d].Rank = %d, want %d", i, standings[i].Rank, want)
		}
	}
}

func TestGetLatestEventID(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	// No data yet — should return 0.
	eventID, err := testStore.GetLatestEventID(ctx, 100)
	if err != nil {
		t.Fatalf("GetLatestEventID: %v", err)
	}
	if eventID != 0 {
		t.Errorf("GetLatestEventID (empty) = %d, want 0", eventID)
	}

	// Insert GW 5.
	if err := testStore.UpsertGameweekStanding(ctx, store.GameweekStanding{
		LeagueID: 100, EventID: 5, ManagerID: 1001, Rank: 1, Points: 15, TotalScore: 200,
	}); err != nil {
		t.Fatalf("UpsertGameweekStanding (GW5): %v", err)
	}
	eventID, err = testStore.GetLatestEventID(ctx, 100)
	if err != nil {
		t.Fatalf("GetLatestEventID after GW5: %v", err)
	}
	if eventID != 5 {
		t.Errorf("GetLatestEventID (after GW5) = %d, want 5", eventID)
	}

	// Insert GW 10 — latest should update.
	if err := testStore.UpsertGameweekStanding(ctx, store.GameweekStanding{
		LeagueID: 100, EventID: 10, ManagerID: 1001, Rank: 1, Points: 30, TotalScore: 400,
	}); err != nil {
		t.Fatalf("UpsertGameweekStanding (GW10): %v", err)
	}
	eventID, err = testStore.GetLatestEventID(ctx, 100)
	if err != nil {
		t.Fatalf("GetLatestEventID after GW10: %v", err)
	}
	if eventID != 10 {
		t.Errorf("GetLatestEventID (after GW10) = %d, want 10", eventID)
	}
}

func TestGetLeagueNotFound(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	_, err := testStore.GetLeague(ctx, 999999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetLeague(nonexistent) error = %v, want store.ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Multi-tenancy tests
// ---------------------------------------------------------------------------

// TestMultiLeagueSameManager proves the schema supports the same FPL
// manager ID appearing in two different leagues. This was the bug the
// Codex review caught — the original schema had a global PK on
// managers.id, which would fail this test.
func TestMultiLeagueSameManager(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "League A", "h2h")
	seedLeague(t, 200, "League B", "h2h")

	// Same FPL manager ID (1001) in both leagues.
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	seedManager(t, 200, 1001, "Alice", "Alice United")

	// Each league should see exactly one manager.
	managersA, err := testStore.GetManagers(ctx, 100)
	if err != nil {
		t.Fatalf("GetManagers(100): %v", err)
	}
	if len(managersA) != 1 || managersA[0].TeamName != "Alice FC" {
		t.Errorf("league 100 managers = %+v, want [Alice FC]", managersA)
	}

	managersB, err := testStore.GetManagers(ctx, 200)
	if err != nil {
		t.Fatalf("GetManagers(200): %v", err)
	}
	if len(managersB) != 1 || managersB[0].TeamName != "Alice United" {
		t.Errorf("league 200 managers = %+v, want [Alice United]", managersB)
	}

	// Standings in different leagues shouldn't interfere.
	if err := testStore.UpsertGameweekStanding(ctx, store.GameweekStanding{
		LeagueID: 100, EventID: 1, ManagerID: 1001, Rank: 1, Points: 10, TotalScore: 100,
	}); err != nil {
		t.Fatalf("UpsertGameweekStanding (league 100): %v", err)
	}
	if err := testStore.UpsertGameweekStanding(ctx, store.GameweekStanding{
		LeagueID: 200, EventID: 1, ManagerID: 1001, Rank: 3, Points: 5, TotalScore: 50,
	}); err != nil {
		t.Fatalf("UpsertGameweekStanding (league 200): %v", err)
	}

	standingsA, err := testStore.GetStandings(ctx, 100, 1)
	if err != nil {
		t.Fatalf("GetStandings(100): %v", err)
	}
	standingsB, err := testStore.GetStandings(ctx, 200, 1)
	if err != nil {
		t.Fatalf("GetStandings(200): %v", err)
	}
	if len(standingsA) != 1 || standingsA[0].Rank != 1 {
		t.Errorf("league 100 standings = %+v, want Rank=1", standingsA)
	}
	if len(standingsB) != 1 || standingsB[0].Rank != 3 {
		t.Errorf("league 200 standings = %+v, want Rank=3", standingsB)
	}
}

// ---------------------------------------------------------------------------
// GetStoredEventIDs tests
// ---------------------------------------------------------------------------

func TestGetStoredEventIDs_Empty(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")

	ids, err := testStore.GetStoredEventIDs(ctx, 100)
	if err != nil {
		t.Fatalf("GetStoredEventIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("len(ids) = %d, want 0", len(ids))
	}
}

func TestGetStoredEventIDs_Multiple(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	// Insert standings for GWs 2, 5, 10 (out of order).
	for _, gw := range []int{5, 2, 10} {
		if err := testStore.UpsertGameweekStanding(ctx, store.GameweekStanding{
			LeagueID: 100, EventID: gw, ManagerID: 1001,
			Rank: 1, Points: 10, TotalScore: 100,
		}); err != nil {
			t.Fatalf("UpsertGameweekStanding (GW%d): %v", gw, err)
		}
	}

	ids, err := testStore.GetStoredEventIDs(ctx, 100)
	if err != nil {
		t.Fatalf("GetStoredEventIDs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("len(ids) = %d, want 3", len(ids))
	}
	// Should be sorted ascending.
	want := []int{2, 5, 10}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], w)
		}
	}
}

func TestGetStoredManagerStatEventIDs_Multiple(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	for _, gw := range []int{8, 3, 5} {
		if err := testStore.UpsertGameweekManagerStat(ctx, store.GameweekManagerStat{
			LeagueID:      100,
			EventID:       gw,
			ManagerID:     1001,
			PointsOnBench: 4,
		}); err != nil {
			t.Fatalf("UpsertGameweekManagerStat (GW%d): %v", gw, err)
		}
	}

	ids, err := testStore.GetStoredManagerStatEventIDs(ctx, 100)
	if err != nil {
		t.Fatalf("GetStoredManagerStatEventIDs: %v", err)
	}
	want := []int{3, 5, 8}
	if len(ids) != len(want) {
		t.Fatalf("len(ids) = %d, want %d", len(ids), len(want))
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], w)
		}
	}
}

func TestGetStoredAwardEventIDs_Multiple(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")

	for _, gw := range []int{7, 2, 9} {
		if err := testStore.UpsertGameweekAward(ctx, store.GameweekAward{
			LeagueID:    100,
			EventID:     gw,
			AwardKey:    "manager_of_the_week",
			ManagerID:   1001,
			MetricValue: 70,
		}); err != nil {
			t.Fatalf("UpsertGameweekAward (GW%d): %v", gw, err)
		}
	}

	ids, err := testStore.GetStoredAwardEventIDs(ctx, 100)
	if err != nil {
		t.Fatalf("GetStoredAwardEventIDs: %v", err)
	}
	want := []int{2, 7, 9}
	if len(ids) != len(want) {
		t.Fatalf("len(ids) = %d, want %d", len(ids), len(want))
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], w)
		}
	}
}

// ---------------------------------------------------------------------------
// UpsertSnapshotMeta / GetSnapshotMeta tests
// ---------------------------------------------------------------------------

func TestUpsertAndGetSnapshotMeta(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()
	seedLeague(t, 100, "Test League", "h2h")

	meta := store.SnapshotMeta{
		LeagueID:          100,
		EventID:           5,
		Source:            "live",
		StandingsFidelity: "historical",
	}

	// Insert.
	if err := testStore.UpsertSnapshotMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertSnapshotMeta: %v", err)
	}

	// Read back.
	got, err := testStore.GetSnapshotMeta(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetSnapshotMeta: %v", err)
	}
	if got.Source != "live" || got.StandingsFidelity != "historical" {
		t.Errorf("got source=%q fidelity=%q, want live/historical", got.Source, got.StandingsFidelity)
	}

	// Upsert with different values (idempotent update).
	meta.Source = "backfill"
	meta.StandingsFidelity = "synthetic"
	if err := testStore.UpsertSnapshotMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertSnapshotMeta (update): %v", err)
	}

	got, err = testStore.GetSnapshotMeta(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetSnapshotMeta after update: %v", err)
	}
	if got.Source != "backfill" || got.StandingsFidelity != "synthetic" {
		t.Errorf("after update: source=%q fidelity=%q, want backfill/synthetic", got.Source, got.StandingsFidelity)
	}
}

func TestGetSnapshotMeta_NotFound(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	_, err := testStore.GetSnapshotMeta(ctx, 999, 1)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetSnapshotMeta(nonexistent) error = %v, want store.ErrNotFound", err)
	}
}

// TestSnapshotAtomicity verifies that a failed transaction doesn't leave
// partial data. We trigger a failure by referencing a manager that doesn't
// exist (FK violation) in the middle of the batch.
func TestSnapshotAtomicity(t *testing.T) {
	truncateTables(t)
	ctx := context.Background()

	seedLeague(t, 100, "Test League", "h2h")
	seedManager(t, 100, 1001, "Alice", "Alice FC")
	// Deliberately NOT seeding manager 9999 — the FK will fail.

	standings := []store.GameweekStanding{
		{LeagueID: 100, EventID: 5, ManagerID: 1001, Rank: 1, Points: 30, TotalScore: 350},
		{LeagueID: 100, EventID: 5, ManagerID: 9999, Rank: 2, Points: 24, TotalScore: 310}, // FK violation
	}

	snap := store.GameweekSnapshot{
		Standings: standings,
		Meta: store.SnapshotMeta{
			LeagueID: 100, EventID: 5,
			Source: "live", StandingsFidelity: "historical",
		},
	}
	err := testStore.SaveGameweekSnapshot(ctx, snap)
	if err == nil {
		t.Fatal("SaveGameweekSnapshot should have failed with FK violation")
	}

	// The first standing (manager 1001) should NOT have been persisted
	// because the entire transaction was rolled back.
	gotStandings, err := testStore.GetStandings(ctx, 100, 5)
	if err != nil {
		t.Fatalf("GetStandings: %v", err)
	}
	if len(gotStandings) != 0 {
		t.Errorf("len(standings) = %d after failed snapshot, want 0 (transaction should have rolled back)", len(gotStandings))
	}

	// Metadata should also NOT have been persisted — it's in the same tx.
	_, err = testStore.GetSnapshotMeta(ctx, 100, 5)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetSnapshotMeta after failed snapshot: got err=%v, want store.ErrNotFound (meta should have been rolled back)", err)
	}
}
