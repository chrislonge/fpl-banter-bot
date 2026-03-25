package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify/telegram"
)

type verificationSummary struct {
	EventID                int
	AlertCount             int
	MessageCount           int
	MaxMessageLength       int
	AwardCount             int
	HasAwards              bool
	HasCaptainGenius       bool
	HasArmbandOfShame      bool
	HasBenchWarmer         bool
	HasUnluckiestLoss      bool
	HasPlotTwist           bool
	MissingCaptainManagers int
	Messages               []string
}

func runVerification(ctx context.Context, appStore store.Store, statsEngine *stats.Engine, leagueID int64, verifyLast int) error {
	eventIDs, err := appStore.GetStoredEventIDs(ctx, leagueID)
	if err != nil {
		return fmt.Errorf("get stored event IDs: %w", err)
	}
	if len(eventIDs) == 0 {
		return fmt.Errorf("no stored events found for league %d", leagueID)
	}

	sort.Ints(eventIDs)
	if verifyLast > 0 && len(eventIDs) > verifyLast {
		eventIDs = eventIDs[len(eventIDs)-verifyLast:]
	}

	slog.Info("starting verification scan", "events", len(eventIDs), "from", eventIDs[0], "to", eventIDs[len(eventIDs)-1])

	summaries := make([]verificationSummary, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		alerts, err := statsEngine.BuildGameweekAlerts(ctx, eventID)
		if err != nil {
			return fmt.Errorf("build alerts for event %d: %w", eventID, err)
		}

		messages, err := telegram.FormatAlerts(alerts)
		if err != nil {
			return fmt.Errorf("format alerts for event %d: %w", eventID, err)
		}

		managerStats, err := appStore.GetGameweekManagerStats(ctx, leagueID, eventID)
		if err != nil {
			return fmt.Errorf("get manager stats for event %d: %w", eventID, err)
		}

		summary := summarizeVerification(eventID, alerts, managerStats, messages)
		summaries = append(summaries, summary)

		fmt.Printf("GW %2d | awards=%d | captain_genius=%t | armband=%t | bench=%t | unluckiest=%t | missing_captains=%d | messages=%d | max_chars=%d\n",
			summary.EventID,
			summary.AwardCount,
			summary.HasCaptainGenius,
			summary.HasArmbandOfShame,
			summary.HasBenchWarmer,
			summary.HasUnluckiestLoss,
			summary.MissingCaptainManagers,
			summary.MessageCount,
			summary.MaxMessageLength,
		)
	}

	printVerificationTotals(summaries)
	printRepresentativePreviews(summaries)
	return nil
}

func summarizeVerification(eventID int, alerts []notify.Alert, managerStats []store.GameweekManagerStat, messages []string) verificationSummary {
	summary := verificationSummary{
		EventID:                eventID,
		AlertCount:             len(alerts),
		MessageCount:           len(messages),
		MaxMessageLength:       maxMessageLength(messages),
		MissingCaptainManagers: countMissingCaptainManagers(managerStats),
		Messages:               messages,
	}

	awards := extractAwards(alerts)
	if awards == nil {
		return summary
	}

	summary.HasAwards = true
	summary.AwardCount = countTrackedAwards(awards)
	summary.HasCaptainGenius = awards.CaptainGenius != nil
	summary.HasArmbandOfShame = awards.ArmbandOfShame != nil
	summary.HasBenchWarmer = awards.BenchWarmer != nil
	summary.HasUnluckiestLoss = awards.UnluckiestLoss != nil
	summary.HasPlotTwist = awards.PlotTwist != nil
	return summary
}

func extractAwards(alerts []notify.Alert) *notify.GameweekAwardsAlert {
	for _, alert := range alerts {
		if alert.Kind == notify.AlertKindGameweekAwards && alert.GameweekAwards != nil {
			return alert.GameweekAwards
		}
	}
	return nil
}

func countTrackedAwards(awards *notify.GameweekAwardsAlert) int {
	if awards == nil {
		return 0
	}

	count := 0
	for _, present := range []bool{
		awards.ManagerOfTheWeek != nil,
		awards.WoodenSpoon != nil,
		awards.CaptainGenius != nil,
		awards.ArmbandOfShame != nil,
		awards.BenchWarmer != nil,
		awards.BiggestThrashing != nil,
		awards.LuckiestWin != nil,
		awards.UnluckiestLoss != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func countMissingCaptainManagers(stats []store.GameweekManagerStat) int {
	missing := 0
	for _, stat := range stats {
		if stat.CaptainElementID == nil || stat.CaptainPoints == nil || stat.CaptainMultiplier == nil {
			missing++
		}
	}
	return missing
}

func maxMessageLength(messages []string) int {
	maxLen := 0
	for _, message := range messages {
		if len(message) > maxLen {
			maxLen = len(message)
		}
	}
	return maxLen
}

func printVerificationTotals(summaries []verificationSummary) {
	total := len(summaries)
	withArmband := 0
	withCaptainGenius := 0
	withBenchWarmer := 0
	withUnluckiestLoss := 0
	withCaptainGaps := 0
	maxAwards := 0

	for _, summary := range summaries {
		if summary.HasArmbandOfShame {
			withArmband++
		}
		if summary.HasCaptainGenius {
			withCaptainGenius++
		}
		if summary.HasBenchWarmer {
			withBenchWarmer++
		}
		if summary.HasUnluckiestLoss {
			withUnluckiestLoss++
		}
		if summary.MissingCaptainManagers > 0 {
			withCaptainGaps++
		}
		if summary.AwardCount > maxAwards {
			maxAwards = summary.AwardCount
		}
	}

	fmt.Printf("\nVerification totals: events=%d captain_genius=%d armband=%d bench=%d unluckiest=%d captain_gaps=%d richest_ceremony=%d awards\n",
		total, withCaptainGenius, withArmband, withBenchWarmer, withUnluckiestLoss, withCaptainGaps, maxAwards)
}

func printRepresentativePreviews(summaries []verificationSummary) {
	if len(summaries) == 0 {
		return
	}

	type preview struct {
		label   string
		summary verificationSummary
	}

	var previews []preview

	if summary, ok := findBestSummary(summaries, func(summary verificationSummary) bool { return summary.AwardCount > 0 }, func(left, right verificationSummary) bool {
		if left.AwardCount != right.AwardCount {
			return left.AwardCount > right.AwardCount
		}
		return left.EventID > right.EventID
	}); ok {
		previews = append(previews, preview{label: "Most complete ceremony", summary: summary})
	}

	if summary, ok := findBestSummary(summaries, func(summary verificationSummary) bool {
		return summary.MissingCaptainManagers > 0
	}, func(left, right verificationSummary) bool {
		if left.MissingCaptainManagers != right.MissingCaptainManagers {
			return left.MissingCaptainManagers > right.MissingCaptainManagers
		}
		return left.EventID < right.EventID
	}); ok {
		previews = append(previews, preview{label: "Captain data gap handling", summary: summary})
	}

	if summary, ok := findBestSummary(summaries, func(summary verificationSummary) bool {
		return summary.HasAwards && !summary.HasArmbandOfShame
	}, func(left, right verificationSummary) bool {
		return left.EventID > right.EventID
	}); ok {
		previews = append(previews, preview{label: "No armband of shame case", summary: summary})
	}

	if summary, ok := findBestSummary(summaries, func(summary verificationSummary) bool {
		return summary.HasUnluckiestLoss
	}, func(left, right verificationSummary) bool {
		return left.EventID > right.EventID
	}); ok {
		previews = append(previews, preview{label: "Unluckiest loss case", summary: summary})
	}

	seen := make(map[int]bool)
	for _, preview := range previews {
		if seen[preview.summary.EventID] {
			continue
		}
		seen[preview.summary.EventID] = true

		fmt.Printf("\n=== %s (GW %d) ===\n", preview.label, preview.summary.EventID)
		for i, message := range preview.summary.Messages {
			fmt.Printf("\n--- Message %d ---\n%s\n", i+1, message)
		}
	}
}

func findBestSummary(summaries []verificationSummary, keep func(verificationSummary) bool, better func(left, right verificationSummary) bool) (verificationSummary, bool) {
	var best verificationSummary
	found := false
	for _, summary := range summaries {
		if !keep(summary) {
			continue
		}
		if !found || better(summary, best) {
			best = summary
			found = true
		}
	}
	return best, found
}
