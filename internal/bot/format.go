// Package bot implements the reactive command handler for the fpl-banter-bot.
//
// This package handles incoming Telegram commands (/standings, /streak,
// /history, /deadline), formats responses, and serves a health endpoint.
// It never imports pkg/notify/telegram directly — all Telegram interactions
// go through consumer-defined interfaces (TelegramBot, etc.).
package bot

import (
	"fmt"
	"html"
	"strings"
	"time"
	"unicode/utf8"

	// Go pattern — BLANK IMPORT FOR SIDE EFFECTS:
	//
	// import _ "time/tzdata" causes the Go compiler to embed the entire IANA
	// timezone database (~450KB) into the binary. Without this, time.LoadLocation
	// reads from the filesystem (/usr/share/zoneinfo), which doesn't exist in
	// scratch-based Docker images.
	//
	// The blank identifier _ means "import for side effects only" — the package's
	// init() function runs (registering the embedded tzdata), but we don't call
	// any of its exported functions directly. This is the same pattern used by
	// database drivers (e.g., import _ "github.com/lib/pq").
	//
	// In Swift, the closest analog is a module that registers itself via a static
	// initializer — you import it, and its side effect (registration) just happens.
	_ "time/tzdata"

	"github.com/chrislonge/fpl-banter-bot/internal/stats"
	"github.com/chrislonge/fpl-banter-bot/internal/store"
	"github.com/chrislonge/fpl-banter-bot/pkg/notify"
)

// formatStandings renders the league standings table for the given gameweek.
//
// The output uses fixed-width formatting to align columns, wrapped in a <pre>
// block for Telegram's monospace rendering. Each row shows rank, manager name,
// H2H points, and total FPL score.
func formatStandings(eventID int, standings []store.GameweekStanding, managers []store.Manager) string {
	if len(standings) == 0 {
		return "No standings data available yet."
	}

	// Build a lookup map for manager names. This avoids an O(n*m) nested
	// loop — same pattern used in the stats engine's getManagerDirectory.
	nameByID := make(map[int64]string, len(managers))
	for _, m := range managers {
		nameByID[m.ID] = m.Name
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("<b>Standings after GW%d</b>\n", eventID))
	b.WriteString("<pre>")
	b.WriteString(fmt.Sprintf("%-4s %-18s %4s %5s\n", "Pos", "Manager", "Pts", "Score"))
	b.WriteString(strings.Repeat("-", 33))

	for _, s := range standings {
		name := nameByID[s.ManagerID]
		if name == "" {
			name = fmt.Sprintf("ID:%d", s.ManagerID)
		}
		// Truncate long names to keep the table aligned.
		// Use truncateUTF8 instead of name[:15] to avoid slicing
		// through multi-byte runes (e.g., accented characters, emoji).
		if len(name) > 18 {
			name = truncateUTF8(name, 15)
		}
		// HTML-escape manager names — names containing <, >, or &
		// would break the surrounding <pre> block.
		b.WriteString(fmt.Sprintf("\n%-4s %-18s %4d %5d",
			ordinal(s.Rank), esc(name), s.Points, s.TotalScore))
	}

	b.WriteString("</pre>")
	return b.String()
}

// formatStreaks renders active win/loss streaks into a readable list.
// Only streaks of length >= 2 are shown to reduce noise.
func formatStreaks(streaks []stats.CurrentStreak) string {
	if len(streaks) == 0 {
		return "No streaks yet this season."
	}

	// Filter to meaningful streaks (2+ matches).
	var meaningful []stats.CurrentStreak
	for _, s := range streaks {
		if s.Length >= 2 {
			meaningful = append(meaningful, s)
		}
	}
	if len(meaningful) == 0 {
		return "No active streaks of 2+ matches."
	}

	var b strings.Builder
	b.WriteString("<b>Active Streaks</b>")

	for _, s := range meaningful {
		name := esc(s.Manager.Name)
		gwRange := fmt.Sprintf("GW%d\u2013%d", s.StartedAt, s.FinishedAt)

		switch s.Kind {
		case notify.StreakKindWin:
			b.WriteString(fmt.Sprintf("\n%s \u2014 %d wins in a row (%s)",
				name, s.Length, gwRange))
		case notify.StreakKindLoss:
			b.WriteString(fmt.Sprintf("\n%s \u2014 %d losses in a row (%s)",
				name, s.Length, gwRange))
		}
	}

	return b.String()
}

// formatH2HRecord renders the lifetime head-to-head record between two
// managers, including wins, draws, losses, and aggregate FPL scores.
func formatH2HRecord(record stats.H2HRecord) string {
	if record.GamesPlayed == 0 {
		return fmt.Sprintf("%s and %s haven't played each other yet.",
			esc(record.ManagerA.Name), esc(record.ManagerB.Name))
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("<b>%s vs %s</b>",
		esc(record.ManagerA.Name), esc(record.ManagerB.Name)))
	b.WriteString(fmt.Sprintf("\nPlayed: %d", record.GamesPlayed))
	b.WriteString(fmt.Sprintf("\n%s wins: %d", esc(record.ManagerA.Name), record.ManagerAWins))
	b.WriteString(fmt.Sprintf("\n%s wins: %d", esc(record.ManagerB.Name), record.ManagerBWins))
	b.WriteString(fmt.Sprintf("\nDraws: %d", record.Draws))
	b.WriteString(fmt.Sprintf("\nTotal pts: %d \u2013 %d",
		record.ManagerAScore, record.ManagerBScore))

	return b.String()
}

// formatDeadline renders the next gameweek deadline in London time.
//
// The time is displayed in Europe/London because FPL is a Premier League
// game and all official deadlines are communicated in UK time. The embedded
// tzdata (from the blank import above) ensures this works in scratch images.
func formatDeadline(name string, t time.Time) string {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		// This should never happen with embedded tzdata, but fail gracefully.
		return fmt.Sprintf("<b>%s</b>\nDeadline: %s", esc(name), t.Format("Mon 2 Jan, 15:04 MST"))
	}
	return fmt.Sprintf("<b>%s</b>\nDeadline: %s",
		esc(name), t.In(london).Format("Mon 2 Jan, 15:04 MST"))
}

// esc escapes HTML special characters in user-supplied text.
//
// This is a local copy of the same helper in pkg/notify/telegram/format.go.
// Duplicating a 1-line function is preferable to coupling internal/bot to
// the telegram package — the bot package must never import a platform-specific
// notifier package (Architecture Rule #2 from CLAUDE.md).
func esc(s string) string {
	return html.EscapeString(s)
}

// truncateUTF8 shortens s to at most maxBytes bytes without splitting
// a multi-byte UTF-8 rune. It appends "..." to indicate truncation.
//
// This is a local copy of the same helper in pkg/notify/telegram/format.go.
// Duplicating a small function is preferable to coupling internal/bot to
// the telegram package (Architecture Rule #2 from CLAUDE.md).
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes] + "..."
}

// ordinal returns the English ordinal for a rank (1st, 2nd, 3rd, etc.).
// Local copy to avoid importing pkg/notify/telegram.
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
