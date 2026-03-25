package main

import (
	"testing"

	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

func TestSummarizeVerification(t *testing.T) {
	chris := notify.ManagerRef{ID: 1, Name: "Chris"}
	dave := notify.ManagerRef{ID: 2, Name: "Dave"}

	alerts := []notify.Alert{
		{
			Kind:     notify.AlertKindGameweekAwards,
			LeagueID: 100,
			EventID:  31,
			GameweekAwards: &notify.GameweekAwardsAlert{
				ManagerOfTheWeek: &notify.ManagerScore{Manager: chris, Score: 76},
				WoodenSpoon:      &notify.ManagerScore{Manager: dave, Score: 21},
				CaptainGenius: &notify.CaptainAwardAlert{
					Manager:           chris,
					Captain:           notify.PlayerRef{ElementID: 10, Name: "B.Fernandes"},
					CaptainPoints:     13,
					CaptainMultiplier: 2,
					TotalPoints:       26,
				},
				BenchWarmer: &notify.BenchWarmerAwardAlert{Manager: dave, PointsOnBench: 17},
				UnluckiestLoss: &notify.UnluckiestLossAlert{
					Loser:         dave,
					LoserScore:    60,
					Opponent:      chris,
					OpponentScore: 65,
					Margin:        5,
				},
			},
		},
	}

	stats := []store.GameweekManagerStat{
		{ManagerID: 1, PointsOnBench: 3, CaptainElementID: intPtr(10), CaptainPoints: intPtr(13), CaptainMultiplier: intPtr(2)},
		{ManagerID: 2, PointsOnBench: 17},
	}

	summary := summarizeVerification(31, alerts, stats, []string{"msg-one", "msg-two"})
	if !summary.HasAwards {
		t.Fatal("expected awards to be present")
	}
	if summary.AwardCount != 5 {
		t.Fatalf("award count = %d, want 5", summary.AwardCount)
	}
	if !summary.HasCaptainGenius || !summary.HasBenchWarmer || !summary.HasUnluckiestLoss {
		t.Fatalf("unexpected summary flags: %+v", summary)
	}
	if summary.HasArmbandOfShame {
		t.Fatalf("expected armband of shame to be absent: %+v", summary)
	}
	if summary.MissingCaptainManagers != 1 {
		t.Fatalf("missing captain managers = %d, want 1", summary.MissingCaptainManagers)
	}
	if summary.MessageCount != 2 {
		t.Fatalf("message count = %d, want 2", summary.MessageCount)
	}
	if summary.MaxMessageLength != len("msg-one") {
		t.Fatalf("max message length = %d, want %d", summary.MaxMessageLength, len("msg-one"))
	}
}

func TestFindBestSummary(t *testing.T) {
	summaries := []verificationSummary{
		{EventID: 28, AwardCount: 4, MissingCaptainManagers: 1},
		{EventID: 30, AwardCount: 6, MissingCaptainManagers: 0},
		{EventID: 31, AwardCount: 7, MissingCaptainManagers: 0},
	}

	best, ok := findBestSummary(summaries,
		func(summary verificationSummary) bool { return summary.AwardCount > 0 },
		func(left, right verificationSummary) bool { return left.AwardCount > right.AwardCount },
	)
	if !ok {
		t.Fatal("expected a best summary")
	}
	if best.EventID != 31 {
		t.Fatalf("best event = %d, want 31", best.EventID)
	}

	gap, ok := findBestSummary(summaries,
		func(summary verificationSummary) bool { return summary.MissingCaptainManagers > 0 },
		func(left, right verificationSummary) bool {
			return left.MissingCaptainManagers > right.MissingCaptainManagers
		},
	)
	if !ok {
		t.Fatal("expected a captain gap summary")
	}
	if gap.EventID != 28 {
		t.Fatalf("captain gap event = %d, want 28", gap.EventID)
	}
}

func intPtr(v int) *int {
	return &v
}
