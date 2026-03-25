package telegram

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func managerRef(id int64, name string) notify.ManagerRef {
	return notify.ManagerRef{ID: id, Name: name, TeamName: name + " FC"}
}

func int64Ptr(v int64) *int64 { return &v }

func h2hAlert(eventID int, leagueID int64, m1 notify.ManagerRef, s1 int, m2 notify.ManagerRef, s2 int, winnerID *int64) notify.Alert {
	return notify.Alert{
		Kind:     notify.AlertKindH2HResult,
		LeagueID: leagueID,
		EventID:  eventID,
		H2HResult: &notify.H2HResultAlert{
			Manager1: m1,
			Score1:   s1,
			Manager2: m2,
			Score2:   s2,
			WinnerID: winnerID,
		},
	}
}

func rankAlert(eventID int, leagueID int64, manager notify.ManagerRef, prev, curr int, movedIntoFirst bool) notify.Alert {
	return notify.Alert{
		Kind:     notify.AlertKindRankChange,
		LeagueID: leagueID,
		EventID:  eventID,
		RankChange: &notify.RankChangeAlert{
			Manager:        manager,
			PreviousRank:   prev,
			CurrentRank:    curr,
			MovedIntoFirst: movedIntoFirst,
		},
	}
}

func streakAlert(eventID int, leagueID int64, manager notify.ManagerRef, kind notify.StreakKind, length, startedAt, finishedAt int) notify.Alert {
	return notify.Alert{
		Kind:     notify.AlertKindStreak,
		LeagueID: leagueID,
		EventID:  eventID,
		Streak: &notify.StreakAlert{
			Manager:    manager,
			Kind:       kind,
			Length:     length,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		},
	}
}

func chipAlert(eventID int, leagueID int64, manager notify.ManagerRef, chip string) notify.Alert {
	return notify.Alert{
		Kind:     notify.AlertKindChipUsage,
		LeagueID: leagueID,
		EventID:  eventID,
		ChipUsage: &notify.ChipUsageAlert{
			Manager: manager,
			Chip:    chip,
		},
	}
}

func awardsAlert(eventID int, leagueID int64, awards *notify.GameweekAwardsAlert) notify.Alert {
	return notify.Alert{
		Kind:           notify.AlertKindGameweekAwards,
		LeagueID:       leagueID,
		EventID:        eventID,
		GameweekAwards: awards,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestFormatAlerts_EmptyInput(t *testing.T) {
	msgs, err := FormatAlerts(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}

	msgs, err = FormatAlerts([]notify.Alert{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}
}

func TestFormatAlerts_MixedEventIDs(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		h2hAlert(10, 100, chris, 65, dave, 42, int64Ptr(1)),
		h2hAlert(11, 100, chris, 55, dave, 50, int64Ptr(1)),
	}

	_, err := FormatAlerts(alerts)
	if err == nil {
		t.Fatal("expected error for mixed event IDs")
	}
	if !strings.Contains(err.Error(), "mixed event IDs") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFormatAlerts_MixedLeagueIDs(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		h2hAlert(10, 100, chris, 65, dave, 42, int64Ptr(1)),
		h2hAlert(10, 200, chris, 55, dave, 50, int64Ptr(1)),
	}

	_, err := FormatAlerts(alerts)
	if err == nil {
		t.Fatal("expected error for mixed league IDs")
	}
	if !strings.Contains(err.Error(), "mixed league IDs") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFormatAlerts_MixedAlertKinds_DisplayOrder(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")
	sarah := managerRef(3, "Sarah")

	// Deliberately emit in non-display order: chip, rank, h2h, streak, awards.
	alerts := []notify.Alert{
		chipAlert(10, 100, sarah, "wildcard"),
		rankAlert(10, 100, chris, 4, 1, true),
		h2hAlert(10, 100, chris, 65, dave, 42, int64Ptr(1)),
		streakAlert(10, 100, chris, notify.StreakKindWin, 4, 7, 10),
		awardsAlert(10, 100, &notify.GameweekAwardsAlert{
			ManagerOfTheWeek: &notify.ManagerScore{Manager: chris, Score: 65},
			WoodenSpoon:      &notify.ManagerScore{Manager: dave, Score: 42},
		}),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}

	combined := strings.Join(msgs, "\n\n")

	// Verify display order: Awards before Results before Table Movers
	// before Streaks before Chips.
	awardsIdx := strings.Index(combined, "<b>Awards Ceremony</b>")
	resultsIdx := strings.Index(combined, "<b>Results</b>")
	moversIdx := strings.Index(combined, "<b>Table Movers</b>")
	streaksIdx := strings.Index(combined, "<b>Streaks</b>")
	chipsIdx := strings.Index(combined, "<b>Chips Played</b>")

	if awardsIdx == -1 || resultsIdx == -1 || moversIdx == -1 || streaksIdx == -1 || chipsIdx == -1 {
		t.Fatalf("missing section(s) in output:\n%s", combined)
	}
	if awardsIdx >= resultsIdx {
		t.Error("Awards Ceremony should appear before Results")
	}
	if resultsIdx >= moversIdx {
		t.Error("Results should appear before Table Movers")
	}
	if moversIdx >= streaksIdx {
		t.Error("Table Movers should appear before Streaks")
	}
	if streaksIdx >= chipsIdx {
		t.Error("Streaks should appear before Chips Played")
	}
}

func TestFormatAlerts_HTMLEscaping(t *testing.T) {
	// Manager names containing HTML special characters.
	dangerous := managerRef(1, "Tom <script>alert('xss')</script>")
	ampersand := managerRef(2, "Dave & Sons")

	alerts := []notify.Alert{
		h2hAlert(10, 100, dangerous, 65, ampersand, 42, int64Ptr(1)),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")

	if strings.Contains(combined, "<script>") {
		t.Error("HTML tags in manager name were not escaped")
	}
	if !strings.Contains(combined, "&lt;script&gt;") {
		t.Error("expected escaped <script> tag in output")
	}
	// Note: strings.Contains(combined, "Dave & Sons") would match even
	// the escaped form "Dave &amp; Sons" because "&amp;" contains "&".
	// So we only assert the positive case — the escaped form is present.
	if !strings.Contains(combined, "Dave &amp; Sons") {
		t.Error("expected & escaped to &amp; in output")
	}
}

func TestFormatAlerts_SingleKindOnly(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		h2hAlert(10, 100, chris, 65, dave, 42, int64Ptr(1)),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if !strings.Contains(msgs[0], "Gameweek 10 Recap") {
		t.Error("expected header in output")
	}
	if !strings.Contains(msgs[0], "<b>Results</b>") {
		t.Error("expected Results section")
	}
	// Should NOT contain other section headers.
	for _, section := range []string{"Awards Ceremony", "Table Movers", "Streaks", "Chips Played"} {
		if strings.Contains(msgs[0], section) {
			t.Errorf("unexpected section %q in single-kind output", section)
		}
	}
}

func TestFormatAlerts_AllKindsPresent(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		awardsAlert(10, 100, &notify.GameweekAwardsAlert{
			ManagerOfTheWeek: &notify.ManagerScore{Manager: chris, Score: 65},
			WoodenSpoon:      &notify.ManagerScore{Manager: dave, Score: 42},
			PlotTwist: &notify.UpsetAlert{
				Winner:             chris,
				WinnerScore:        65,
				WinnerPreviousRank: 3,
				Loser:              dave,
				LoserScore:         42,
				LoserPreviousRank:  1,
				RankGap:            2,
			},
		}),
		h2hAlert(10, 100, chris, 65, dave, 42, int64Ptr(1)),
		rankAlert(10, 100, chris, 3, 1, true),
		streakAlert(10, 100, chris, notify.StreakKindWin, 5, 6, 10),
		chipAlert(10, 100, dave, "bboost"),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")

	// All sections present.
	for _, section := range []string{"Awards Ceremony", "Results", "Table Movers", "Streaks", "Chips Played"} {
		if !strings.Contains(combined, section) {
			t.Errorf("missing section: %s", section)
		}
	}

	// Banter content checks.
	if !strings.Contains(combined, "claimed the throne") {
		t.Error("expected throne banter for moving into 1st")
	}
	if !strings.Contains(combined, "on fire") {
		t.Error("expected fire banter for 5+ win streak")
	}
	if !strings.Contains(combined, "Bench Boost") {
		t.Error("expected human-readable chip name for bboost")
	}
	if !strings.Contains(combined, "took the spoon") {
		t.Error("expected awards copy for wooden spoon")
	}
	if !strings.Contains(combined, "Plot twist:") {
		t.Error("expected plot twist line in awards")
	}
}

func TestFormatAlerts_AwardsIncludePlotTwist(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		h2hAlert(10, 100, chris, 65, dave, 42, int64Ptr(1)),
		awardsAlert(10, 100, &notify.GameweekAwardsAlert{
			ManagerOfTheWeek: &notify.ManagerScore{Manager: chris, Score: 65},
			WoodenSpoon:      &notify.ManagerScore{Manager: dave, Score: 42},
			CaptainGenius: &notify.CaptainAwardAlert{
				Manager:           chris,
				Captain:           notify.PlayerRef{ElementID: 1, Name: "B.Fernandes"},
				CaptainPoints:     13,
				CaptainMultiplier: 2,
				TotalPoints:       26,
			},
			ArmbandOfShame: &notify.ArmbandOfShameAlert{
				Manager:          dave,
				Captain:          notify.PlayerRef{ElementID: 2, Name: "Haaland"},
				CaptainPoints:    0,
				ConsensusCaptain: notify.PlayerRef{ElementID: 1, Name: "B.Fernandes"},
				ConsensusPoints:  13,
			},
			BenchWarmer: &notify.BenchWarmerAwardAlert{
				Manager:       dave,
				PointsOnBench: 17,
			},
			BiggestThrashing: &notify.MatchupAwardAlert{
				Winner:      chris,
				WinnerScore: 65,
				Loser:       dave,
				LoserScore:  42,
				Margin:      23,
			},
			LuckiestWin: &notify.MatchupAwardAlert{
				Winner:      chris,
				WinnerScore: 38,
				Loser:       dave,
				LoserScore:  33,
				Margin:      5,
			},
			UnluckiestLoss: &notify.UnluckiestLossAlert{
				Loser:         dave,
				LoserScore:    60,
				Opponent:      chris,
				OpponentScore: 65,
				Margin:        5,
			},
			PlotTwist: &notify.UpsetAlert{
				Winner:             chris,
				WinnerScore:        65,
				WinnerPreviousRank: 5,
				Loser:              dave,
				LoserScore:         42,
				LoserPreviousRank:  2,
				RankGap:            3,
			},
		}),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")
	awardsIdx := strings.Index(combined, "<b>Awards Ceremony</b>")
	resultsIdx := strings.Index(combined, "<b>Results</b>")
	if awardsIdx == -1 || resultsIdx == -1 {
		t.Fatalf("expected awards and results sections in output:\n%s", combined)
	}
	if awardsIdx >= resultsIdx {
		t.Fatal("Awards Ceremony should appear before Results")
	}

	for _, want := range []string{
		"💩 Wooden Spoon: <b>Dave</b> took the spoon with 42 pts",
		"🎯 Captain Genius: <b>Chris</b> backed <b>B.Fernandes</b> for 26 pts",
		"🤡 Armband of Shame: <b>Dave</b> trusted <b>Haaland</b> (0 pts) while <b>B.Fernandes</b> hauled 13",
		"🪑 Bench Warmer: <b>Dave</b> stranded 17 pts on the bench",
		"💀 Biggest Thrashing: <b>Chris</b> steamrolled Dave 65-42",
		"🎰 Luckiest Win: <b>Chris</b> snuck past Dave 38-33",
		"😤 Unluckiest Loss: <b>Dave</b> lost 60-65 to Chris and still beats everyone else this week",
		"Plot twist: 5th-place Chris mugged 2nd-place Dave (65-42)",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("expected %q in output:\n%s", want, combined)
		}
	}
}

// ---------------------------------------------------------------------------
// Banter-specific tests
// ---------------------------------------------------------------------------

func TestFormatAlerts_H2HResult_Draw(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		h2hAlert(10, 100, chris, 55, dave, 55, nil),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")
	if !strings.Contains(combined, "couldn't separate them") {
		t.Error("expected draw banter")
	}
}

func TestFormatAlerts_H2HResult_MassiveWin(t *testing.T) {
	chris := managerRef(1, "Chris")
	dave := managerRef(2, "Dave")

	// 30+ point margin triggers "put them to the sword".
	alerts := []notify.Alert{
		h2hAlert(10, 100, chris, 80, dave, 40, int64Ptr(1)),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")
	if !strings.Contains(combined, "put them to the sword") {
		t.Error("expected sword banter for 30+ point margin")
	}
}

func TestFormatAlerts_Streak_LossStreak(t *testing.T) {
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		streakAlert(10, 100, dave, notify.StreakKindLoss, 3, 8, 10),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")
	if !strings.Contains(combined, "freefall") {
		t.Error("expected freefall banter for loss streak")
	}
	if !strings.Contains(combined, "lost 3 on the bounce") {
		t.Error("expected bounce banter for loss streak")
	}
}

func TestFormatAlerts_RankChange_Dropped(t *testing.T) {
	dave := managerRef(2, "Dave")

	alerts := []notify.Alert{
		rankAlert(10, 100, dave, 2, 5, false),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := strings.Join(msgs, "\n\n")
	if !strings.Contains(combined, "dropped from 2nd to 5th") {
		t.Error("expected drop description")
	}
}

func TestFormatAlerts_ChipDisplayNames(t *testing.T) {
	chris := managerRef(1, "Chris")

	tests := []struct {
		chip     string
		expected string
	}{
		{"wildcard", "Wildcard"},
		{"freehit", "Free Hit"},
		{"bboost", "Bench Boost"},
		{"3xc", "Triple Captain"},
		{"unknown_chip", "unknown_chip"},
	}

	for _, tt := range tests {
		t.Run(tt.chip, func(t *testing.T) {
			alerts := []notify.Alert{chipAlert(10, 100, chris, tt.chip)}
			msgs, err := FormatAlerts(alerts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			combined := strings.Join(msgs, "\n\n")
			if !strings.Contains(combined, tt.expected) {
				t.Errorf("expected %q in output, got:\n%s", tt.expected, combined)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Chunking tests
// ---------------------------------------------------------------------------

func TestFormatAlerts_JustBelowLimit(t *testing.T) {
	// Generate enough alerts to stay just under 4096 chars.
	var alerts []notify.Alert
	for i := 0; i < 20; i++ {
		m1 := managerRef(int64(i*2+1), fmt.Sprintf("Player%d", i*2+1))
		m2 := managerRef(int64(i*2+2), fmt.Sprintf("Player%d", i*2+2))
		alerts = append(alerts, h2hAlert(10, 100, m1, 50+i, m2, 40+i, int64Ptr(m1.ID)))
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With 20 short results, this should fit in a single message.
	if len(msgs) == 1 && len(msgs[0]) > telegramMaxMessageLength {
		t.Errorf("single message exceeds limit: %d chars", len(msgs[0]))
	}
}

func TestFormatAlerts_JustAboveLimit_SplitsAtSectionBoundary(t *testing.T) {
	// Create two large sections that together exceed 4096 chars.
	var alerts []notify.Alert

	// ~80 H2H results to fill one large section.
	for i := 0; i < 80; i++ {
		m1 := managerRef(int64(i*2+1), fmt.Sprintf("LongPlayerName%03d", i*2+1))
		m2 := managerRef(int64(i*2+2), fmt.Sprintf("LongPlayerName%03d", i*2+2))
		alerts = append(alerts, h2hAlert(10, 100, m1, 50+i, m2, 40+i, int64Ptr(m1.ID)))
	}

	// Add rank changes to create a second section.
	for i := 0; i < 30; i++ {
		m := managerRef(int64(i+1), fmt.Sprintf("LongPlayerName%03d", i+1))
		alerts = append(alerts, rankAlert(10, 100, m, i+2, i+1, false))
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) < 2 {
		t.Fatalf("expected multiple messages due to size, got %d", len(msgs))
	}

	for i, m := range msgs {
		if len(m) > telegramMaxMessageLength {
			t.Errorf("message %d exceeds limit: %d chars", i, len(m))
		}
	}
}

func TestFormatAlerts_OversizedSingleSection_SplitsByLine(t *testing.T) {
	// Create a single section with enough alerts to exceed 4096 chars.
	var alerts []notify.Alert
	for i := 0; i < 120; i++ {
		m1 := managerRef(int64(i*2+1), fmt.Sprintf("VeryLongPlayerNameForTesting%03d", i*2+1))
		m2 := managerRef(int64(i*2+2), fmt.Sprintf("VeryLongPlayerNameForTesting%03d", i*2+2))
		alerts = append(alerts, h2hAlert(10, 100, m1, 50+i, m2, 40+i, int64Ptr(m1.ID)))
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) < 2 {
		t.Fatal("expected multiple messages for oversized single section")
	}

	for i, m := range msgs {
		if len(m) > telegramMaxMessageLength {
			t.Errorf("message %d exceeds limit: %d chars", i, len(m))
		}
	}
}

func TestFormatAlerts_OversizedSingleLine_Truncates(t *testing.T) {
	// Create a manager name so long that a single rendered line exceeds 4096.
	longName := strings.Repeat("A", 5000)
	m1 := managerRef(1, longName)
	m2 := managerRef(2, "Dave")

	alerts := []notify.Alert{
		h2hAlert(10, 100, m1, 65, m2, 42, int64Ptr(1)),
	}

	msgs, err := FormatAlerts(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, m := range msgs {
		if len(m) > telegramMaxMessageLength {
			t.Errorf("message %d exceeds limit: %d chars", i, len(m))
		}
	}

	combined := strings.Join(msgs, "")
	if !strings.Contains(combined, "...") {
		t.Error("expected truncation indicator '...' for oversized line")
	}
}

// ---------------------------------------------------------------------------
// Ordinal helper tests
// ---------------------------------------------------------------------------

func TestOrdinal(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{1, "1st"},
		{2, "2nd"},
		{3, "3rd"},
		{4, "4th"},
		{10, "10th"},
		{11, "11th"},
		{12, "12th"},
		{13, "13th"},
		{21, "21st"},
		{22, "22nd"},
		{23, "23rd"},
		{111, "111th"},
		{112, "112th"},
		{113, "113th"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := ordinal(tt.input); got != tt.expected {
				t.Errorf("ordinal(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
