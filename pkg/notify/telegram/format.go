// Package telegram implements the notify.Notifier interface for Telegram.
//
// This package is the rendering layer — it decides HOW alerts look in a
// Telegram chat. The stats engine decides WHAT is alert-worthy; this package
// decides how to phrase it with spicy banter.
package telegram

import (
	"fmt"
	"html"
	"strings"

	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// telegramMaxMessageLength is the maximum number of characters Telegram
// allows in a single message. Messages exceeding this limit must be split.
const telegramMaxMessageLength = 4096

// FormatAlerts renders a slice of alerts into one or more HTML-formatted
// Telegram messages.
//
// The function matches the notify.Notifier boundary: it accepts the same
// []notify.Alert that SendAlerts receives. Each alert already carries
// EventID and LeagueID, so there is no separate eventID parameter —
// single source of truth.
//
// Input validation:
//   - Empty input returns (nil, nil).
//   - All alerts must share the same EventID — returns error if mixed.
//   - All alerts must share the same LeagueID — returns error if mixed.
//
// The formatter regroups alerts by kind regardless of emission order from
// the stats engine, rendering sections in a fixed display order.
func FormatAlerts(alerts []notify.Alert) ([]string, error) {
	if len(alerts) == 0 {
		return nil, nil
	}

	eventID := alerts[0].EventID
	leagueID := alerts[0].LeagueID

	for _, a := range alerts[1:] {
		if a.EventID != eventID {
			return nil, fmt.Errorf("mixed event IDs: got %d and %d", eventID, a.EventID)
		}
		if a.LeagueID != leagueID {
			return nil, fmt.Errorf("mixed league IDs: got %d and %d", leagueID, a.LeagueID)
		}
	}

	// Bucket alerts by kind. The stats engine may emit them in any order;
	// the formatter controls display order.
	var (
		h2hResults  []notify.Alert
		rankChanges []notify.Alert
		streaks     []notify.Alert
		chips       []notify.Alert
		summaries   []notify.Alert
	)
	for _, a := range alerts {
		switch a.Kind {
		case notify.AlertKindH2HResult:
			h2hResults = append(h2hResults, a)
		case notify.AlertKindRankChange:
			rankChanges = append(rankChanges, a)
		case notify.AlertKindStreak:
			streaks = append(streaks, a)
		case notify.AlertKindChipUsage:
			chips = append(chips, a)
		case notify.AlertKindGameweekSummary:
			summaries = append(summaries, a)
		}
	}

	// Build sections in display order. Each section is a self-contained
	// block of HTML text that can be split independently if needed.
	header := fmt.Sprintf("<b>Gameweek %d Recap</b>", eventID)

	var sections []string
	if lines := formatH2HResults(h2hResults); lines != "" {
		sections = append(sections, lines)
	}
	if lines := formatRankChanges(rankChanges); lines != "" {
		sections = append(sections, lines)
	}
	if lines := formatStreaks(streaks); lines != "" {
		sections = append(sections, lines)
	}
	if lines := formatChips(chips); lines != "" {
		sections = append(sections, lines)
	}
	if lines := formatSummaries(summaries); lines != "" {
		sections = append(sections, lines)
	}

	if len(sections) == 0 {
		return nil, nil
	}

	return chunkMessages(header, sections), nil
}

// chunkMessages combines a header and sections into messages that each fit
// within Telegram's character limit.
//
// Chunking strategy (3-tier fallback):
//  1. Prefer splitting at section boundaries.
//  2. If a single section exceeds the limit, split by rendered alert line.
//  3. If a single rendered line exceeds the limit, truncate safely.
func chunkMessages(header string, sections []string) []string {
	var messages []string
	current := header

	for _, section := range sections {
		candidate := current + "\n\n" + section
		if len(candidate) <= telegramMaxMessageLength {
			current = candidate
			continue
		}

		// The combined message is too large. Flush what we have (if any
		// content beyond the header) and start a new message.
		if current != header {
			messages = append(messages, current)
			current = header
		}

		// Check if this section alone (with header) fits in one message.
		candidate = current + "\n\n" + section
		if len(candidate) <= telegramMaxMessageLength {
			current = candidate
			continue
		}

		// Tier 2: section too large — split by individual lines.
		lines := strings.Split(section, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			candidate = current + "\n" + line
			if len(candidate) <= telegramMaxMessageLength {
				current = candidate
				continue
			}

			// Flush current if it has content beyond the header.
			if current != header {
				messages = append(messages, current)
				current = header
			}

			// Tier 3: single line exceeds limit — truncate.
			lineWithHeader := current + "\n" + line
			if len(lineWithHeader) > telegramMaxMessageLength {
				maxLineLen := telegramMaxMessageLength - len(current) - len("\n") - len("...")
				if maxLineLen > 0 {
					line = line[:maxLineLen] + "..."
				}
			}
			current = current + "\n" + line
		}
	}

	if current != "" {
		messages = append(messages, current)
	}

	return messages
}

// ---------------------------------------------------------------------------
// Section formatters — one per AlertKind
// ---------------------------------------------------------------------------

func formatH2HResults(alerts []notify.Alert) string {
	if len(alerts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<b>Results</b>")

	for _, a := range alerts {
		r := a.H2HResult
		if r == nil {
			continue
		}

		name1 := esc(r.Manager1.Name)
		name2 := esc(r.Manager2.Name)

		switch {
		case r.WinnerID == nil:
			// Draw.
			b.WriteString(fmt.Sprintf("\n%s %d - %d %s — couldn't separate them",
				name1, r.Score1, r.Score2, name2))

		case *r.WinnerID == r.Manager1.ID:
			diff := r.Score1 - r.Score2
			b.WriteString("\n")
			b.WriteString(formatWinLine(name1, r.Score1, name2, r.Score2, diff))

		default:
			diff := r.Score2 - r.Score1
			b.WriteString("\n")
			b.WriteString(formatWinLine(name2, r.Score2, name1, r.Score1, diff))
		}
	}

	return b.String()
}

func formatWinLine(winner string, winScore int, loser string, loseScore int, diff int) string {
	if diff >= 30 {
		return fmt.Sprintf("<b>%s</b> %d - %d %s — put them to the sword",
			winner, winScore, loseScore, loser)
	}
	return fmt.Sprintf("<b>%s</b> %d - %d %s",
		winner, winScore, loseScore, loser)
}

func formatRankChanges(alerts []notify.Alert) string {
	if len(alerts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<b>Table Movers</b>")

	for _, a := range alerts {
		rc := a.RankChange
		if rc == nil {
			continue
		}

		name := esc(rc.Manager.Name)

		switch {
		case rc.MovedIntoFirst:
			b.WriteString(fmt.Sprintf("\n%s has claimed the throne \xf0\x9f\x91\x91 (was %s)",
				name, ordinal(rc.PreviousRank)))

		case rc.CurrentRank < rc.PreviousRank:
			b.WriteString(fmt.Sprintf("\n%s climbed from %s to %s",
				name, ordinal(rc.PreviousRank), ordinal(rc.CurrentRank)))

		default:
			b.WriteString(fmt.Sprintf("\n%s dropped from %s to %s",
				name, ordinal(rc.PreviousRank), ordinal(rc.CurrentRank)))
		}
	}

	return b.String()
}

func formatStreaks(alerts []notify.Alert) string {
	if len(alerts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<b>Streaks</b>")

	for _, a := range alerts {
		s := a.Streak
		if s == nil {
			continue
		}

		name := esc(s.Manager.Name)
		gwRange := fmt.Sprintf("GW%d–%d", s.StartedAt, s.FinishedAt)

		switch s.Kind {
		case notify.StreakKindWin:
			if s.Length >= 5 {
				b.WriteString(fmt.Sprintf("\n%s is absolutely on fire \xf0\x9f\x94\xa5 — %d wins in a row (%s)",
					name, s.Length, gwRange))
			} else {
				b.WriteString(fmt.Sprintf("\n%s on a %d-match winning streak (%s)",
					name, s.Length, gwRange))
			}
		case notify.StreakKindLoss:
			b.WriteString(fmt.Sprintf("\n%s in freefall — lost %d on the bounce (%s)",
				name, s.Length, gwRange))
		}
	}

	return b.String()
}

func formatChips(alerts []notify.Alert) string {
	if len(alerts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<b>Chips Played</b>")

	for _, a := range alerts {
		c := a.ChipUsage
		if c == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("\n%s activated <b>%s</b>",
			esc(c.Manager.Name), esc(chipDisplayName(c.Chip))))
	}

	return b.String()
}

func formatSummaries(alerts []notify.Alert) string {
	if len(alerts) == 0 {
		return ""
	}

	// There should only be one summary per gameweek, but handle gracefully.
	a := alerts[0]
	s := a.GameweekSummary
	if s == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("<b>Summary</b>")
	b.WriteString(fmt.Sprintf("\nHighest scorer: <b>%s</b> (%d pts)",
		esc(s.HighScorer.Manager.Name), s.HighScorer.Score))
	b.WriteString(fmt.Sprintf("\nWooden spoon \xf0\x9f\xa5\x84 goes to %s (%d pts)",
		esc(s.LowScorer.Manager.Name), s.LowScorer.Score))

	if s.BiggestUpset != nil {
		u := s.BiggestUpset
		b.WriteString(fmt.Sprintf("\nBiggest upset: %s-place %s got absolutely mugged by %s-place %s (%d-%d)",
			ordinal(u.LoserPreviousRank), esc(u.Loser.Name),
			ordinal(u.WinnerPreviousRank), esc(u.Winner.Name),
			u.WinnerScore, u.LoserScore))
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// esc escapes HTML special characters in user-supplied text.
// Telegram HTML mode requires escaping <, >, and &.
func esc(s string) string {
	return html.EscapeString(s)
}

// ordinal returns the English ordinal for a rank (1st, 2nd, 3rd, etc.).
func ordinal(n int) string {
	suffix := "th"
	switch n % 10 {
	case 1:
		if n%100 != 11 {
			suffix = "st"
		}
	case 2:
		if n%100 != 12 {
			suffix = "nd"
		}
	case 3:
		if n%100 != 13 {
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// chipDisplayName converts FPL's internal chip identifier to a
// human-readable display name.
func chipDisplayName(chip string) string {
	switch strings.ToLower(chip) {
	case "wildcard":
		return "Wildcard"
	case "freehit":
		return "Free Hit"
	case "bboost":
		return "Bench Boost"
	case "3xc":
		return "Triple Captain"
	default:
		return chip
	}
}
