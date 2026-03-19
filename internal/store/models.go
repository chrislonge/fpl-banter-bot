// Package store provides the persistence layer for fpl-banter-bot.
//
// These types are the store's own domain models — they map 1:1 to the
// database schema, NOT to the FPL API response types in internal/fpl/.
// This separation is Dependency Inversion: each layer defines its own
// contracts. The mapping between FPL API types and store types happens
// in the coordinator/poller layer (Phase 1.4), keeping both packages
// independent of each other.
package store

import "time"

// League represents a tracked FPL league.
//
// The id comes from the FPL API (e.g., 916670). It's stored as int64
// because the database column is BIGINT. In Go, the plain `int` type
// varies by platform (32-bit on 32-bit systems, 64-bit on 64-bit),
// so int64 guarantees correctness everywhere — including the ARM64
// Raspberry Pi this bot targets.
type League struct {
	ID        int64     // FPL league ID (BIGINT in Postgres)
	Name      string    // League display name
	Type      string    // "h2h" or "classic"
	CreatedAt time.Time // Row creation timestamp (TIMESTAMPTZ)
}

// Manager represents an FPL manager within a specific league.
//
// The composite primary key (LeagueID, ID) supports multi-tenancy: the
// same FPL manager can appear in multiple leagues. Without this, adding
// a second league that shares a manager would violate a unique constraint.
//
// time.Time is the correct Go type for Postgres TIMESTAMPTZ. The pgx
// driver auto-scans Postgres timestamps into time.Time, giving you
// type-safe comparisons, timezone handling, and formatting for free.
type Manager struct {
	LeagueID  int64     // FK to leagues — part of the composite PK
	ID        int64     // FPL manager/entry ID — part of the composite PK
	Name      string    // Manager's real name
	TeamName  string    // Fantasy team name
	CreatedAt time.Time // Row creation timestamp
}

// GameweekStanding records a manager's position in the league table
// after a specific gameweek. The composite PK (league_id, event_id,
// manager_id) makes each row idempotent — upserting the same gameweek
// twice updates rather than duplicates.
type GameweekStanding struct {
	LeagueID   int64     // FK to leagues
	EventID    int       // Gameweek number (1–38)
	ManagerID  int64     // FK to managers
	Rank       int       // Position in the league table
	Points     int       // H2H points (W=3, D=1, L=0)
	TotalScore int       // Cumulative FPL points scored
	CreatedAt  time.Time // Row creation timestamp
}

// ChipUsage records when a manager played a chip (wildcard, bench boost,
// etc.) in a specific gameweek. The PK includes the chip name because a
// manager could theoretically use different chips in different contexts
// (though in practice each GW has at most one chip).
//
// Unlike other tables, chip_usage uses ON CONFLICT DO NOTHING — once a
// chip usage is detected, it never changes. This is an immutable fact.
type ChipUsage struct {
	LeagueID   int64     // FK to leagues
	ManagerID  int64     // FK to managers
	EventID    int       // Gameweek the chip was played
	Chip       string    // "bboost", "3xc", "freehit", "wildcard"
	DetectedAt time.Time // When we first detected this chip usage
}

// H2HResult records the outcome of a head-to-head match between two
// managers in a specific gameweek.
//
// The CHECK constraint (manager_1_id < manager_2_id) enforces canonical
// ordering: the manager with the lower ID is always manager_1. This
// prevents the same match from being stored twice with swapped IDs.
// The poller must sort the IDs before inserting.
type H2HResult struct {
	LeagueID      int64     // FK to leagues
	EventID       int       // Gameweek number
	Manager1ID    int64     // Lower manager ID (enforced by CHECK constraint)
	Manager1Score int       // Manager 1's GW score
	Manager2ID    int64     // Higher manager ID
	Manager2Score int       // Manager 2's GW score
	CreatedAt     time.Time // Row creation timestamp
}
