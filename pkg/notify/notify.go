// Package notify defines the Notifier interface for sending alerts to chat
// platforms. Platform-specific implementations (Telegram, Discord, etc.)
// live in subdirectories.
//
// This package is in pkg/ (not internal/) because it is the public extension
// point — anyone building a new chat adapter imports this interface.
package notify

import "context"

// AlertKind categorises a banter-worthy event detected by the stats engine.
type AlertKind string

const (
	AlertKindGameweekAwards AlertKind = "gameweek_awards"
	AlertKindRankChange     AlertKind = "rank_change"
	AlertKindStreak         AlertKind = "streak"
	AlertKindChipUsage      AlertKind = "chip_usage"
	AlertKindH2HResult      AlertKind = "h2h_result"
)

// StreakKind identifies the type of streak.
type StreakKind string

const (
	StreakKindWin  StreakKind = "win"
	StreakKindLoss StreakKind = "loss"
)

// ManagerRef carries the minimum identity a notifier needs to describe a
// manager without making another store lookup.
type ManagerRef struct {
	ID       int64
	Name     string
	TeamName string
}

// ManagerScore pairs a manager with a single gameweek score.
type ManagerScore struct {
	Manager ManagerRef
	Score   int
}

// PlayerRef carries the minimum player identity needed for rendering awards.
type PlayerRef struct {
	ElementID int
	Name      string
}

// Alert is a structured event emitted by the stats engine.
//
// Exactly one detail payload should be populated based on Kind. Keeping the
// payload structured lets the notifier decide how to render the alert for a
// specific chat platform without re-querying business data.
type Alert struct {
	Kind     AlertKind
	LeagueID int64
	EventID  int

	RankChange     *RankChangeAlert
	Streak         *StreakAlert
	ChipUsage      *ChipUsageAlert
	GameweekAwards *GameweekAwardsAlert
	H2HResult      *H2HResultAlert
}

// RankChangeAlert reports a movement in the league table relative to the
// previous gameweek.
type RankChangeAlert struct {
	Manager        ManagerRef
	PreviousRank   int
	CurrentRank    int
	MovedIntoFirst bool
}

// StreakAlert reports a current win or loss streak.
type StreakAlert struct {
	Manager    ManagerRef
	Kind       StreakKind
	Length     int
	StartedAt  int
	FinishedAt int
}

// ChipUsageAlert reports a chip played in a specific gameweek.
type ChipUsageAlert struct {
	Manager ManagerRef
	Chip    string
}

// CaptainAwardAlert reports a captain-based award.
type CaptainAwardAlert struct {
	Manager           ManagerRef
	Captain           PlayerRef
	CaptainPoints     int
	CaptainMultiplier int
	TotalPoints       int
}

// ArmbandOfShameAlert reports a captain miss against the league consensus.
type ArmbandOfShameAlert struct {
	Manager          ManagerRef
	Captain          PlayerRef
	CaptainPoints    int
	ConsensusCaptain PlayerRef
	ConsensusPoints  int
}

// BenchWarmerAwardAlert reports the most points stranded on the bench.
type BenchWarmerAwardAlert struct {
	Manager       ManagerRef
	PointsOnBench int
}

// MatchupAwardAlert reports a matchup-based award with winner/loser context.
type MatchupAwardAlert struct {
	Winner      ManagerRef
	WinnerScore int
	Loser       ManagerRef
	LoserScore  int
	Margin      int
}

// UnluckiestLossAlert reports a strong losing score that ran into one
// exceptional opponent.
type UnluckiestLossAlert struct {
	Loser         ManagerRef
	LoserScore    int
	Opponent      ManagerRef
	OpponentScore int
	Margin        int
}

// UpsetAlert reports a lower-ranked manager beating a higher-ranked one.
type UpsetAlert struct {
	Winner             ManagerRef
	WinnerScore        int
	WinnerPreviousRank int
	Loser              ManagerRef
	LoserScore         int
	LoserPreviousRank  int
	RankGap            int
}

// GameweekAwardsAlert reports the headline awards for a gameweek.
type GameweekAwardsAlert struct {
	ManagerOfTheWeek *ManagerScore
	WoodenSpoon      *ManagerScore
	CaptainGenius    *CaptainAwardAlert
	ArmbandOfShame   *ArmbandOfShameAlert
	BenchWarmer      *BenchWarmerAwardAlert
	BiggestThrashing *MatchupAwardAlert
	LuckiestWin      *MatchupAwardAlert
	UnluckiestLoss   *UnluckiestLossAlert
	PlotTwist        *UpsetAlert
}

// H2HResultAlert reports the outcome of a single head-to-head fixture.
type H2HResultAlert struct {
	Manager1 ManagerRef
	Score1   int
	Manager2 ManagerRef
	Score2   int
	WinnerID *int64
}

// Notifier sends alerts to a chat platform. Implementations must be safe for
// concurrent use (the poller may call from a goroutine).
//
// In Go, interfaces are satisfied implicitly — a struct just needs to have
// the right method signatures. No "implements" keyword required.
type Notifier interface {
	// SendAlerts delivers one or more alerts to the configured chat.
	// context.Context is the first parameter by convention — it carries
	// deadlines, cancellation signals, and request-scoped values. Any
	// function that does I/O (network, disk, DB) should accept a context.
	SendAlerts(ctx context.Context, alerts []Alert) error
}
