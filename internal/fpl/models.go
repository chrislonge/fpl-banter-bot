// Package fpl provides an HTTP client for the Fantasy Premier League API.
//
// The FPL API is unofficial and undocumented — it could change at any time.
// This package isolates all API interaction behind typed Go structs and a
// clean client interface, so changes to the API only require updating this
// one package.
//
// This package lives in internal/ because it is an implementation detail of
// the bot. Other packages in this module import it, but external code cannot
// (the Go compiler enforces this).
package fpl

// ---------------------------------------------------------------------------
// Bootstrap (GET /bootstrap-static/)
// ---------------------------------------------------------------------------
//
// This is the largest response (~1.3 MB). It contains all gameweeks, all
// Premier League teams, and all players. We only decode the fields we need.
//
// Go concept — STRUCT TAGS:
//
//	type Event struct {
//	    ID int `json:"id"`
//	}
//
// The backtick-delimited string after a field is a "struct tag." The
// encoding/json package reads these tags to know which JSON key maps to
// which Go field. Without the tag, the decoder does a case-insensitive
// match on the field name, which is fragile and should never be relied on.
//
// Go concept — SELECTIVE DECODING:
//
// encoding/json silently ignores JSON keys that don't have a matching
// struct field. This means we only need to define fields we actually use.
// The ~1.3 MB bootstrap response has hundreds of fields per player — we
// skip all of them because we don't need player-level data yet.
// ---------------------------------------------------------------------------

// BootstrapResponse is the top-level response from /bootstrap-static/.
// We only extract the events (gameweeks) and teams arrays.
type BootstrapResponse struct {
	Events []Event `json:"events"`
	Teams  []Team  `json:"teams"`
}

// Event represents a single gameweek in the FPL season.
type Event struct {
	ID           int        `json:"id"`                  // Gameweek number (1–38)
	Name         string     `json:"name"`                // e.g., "Gameweek 1"
	DeadlineTime string     `json:"deadline_time"`       // ISO 8601 timestamp
	Finished     bool       `json:"finished"`            // All fixtures in this GW are done
	DataChecked  bool       `json:"data_checked"`        // FPL has validated the data
	IsPrevious   bool       `json:"is_previous"`         // This is the most recently completed GW
	IsCurrent    bool       `json:"is_current"`          // This GW is currently in progress
	IsNext       bool       `json:"is_next"`             // This is the upcoming GW
	AverageScore int        `json:"average_entry_score"` // Mean score across all FPL managers
	HighestScore int        `json:"highest_score"`       // Highest individual GW score
	MostCaptained int       `json:"most_captained"`      // Player ID most often captained
	ChipPlays    []ChipPlay `json:"chip_plays"`          // Aggregate chip usage stats
}

// ChipPlay records how many managers used a particular chip in a gameweek.
type ChipPlay struct {
	ChipName  string `json:"chip_name"`  // "bboost", "3xc", "freehit", "wildcard"
	NumPlayed int    `json:"num_played"` // Number of managers who played this chip
}

// Team represents a Premier League club.
type Team struct {
	ID        int    `json:"id"`         // Unique team ID (1–20)
	Name      string `json:"name"`       // Full name, e.g., "Arsenal"
	ShortName string `json:"short_name"` // 3-letter code, e.g., "ARS"
	Code      int    `json:"code"`       // Internal FPL code
	Strength  int    `json:"strength"`   // Overall strength rating
	Position  int    `json:"position"`   // Current Premier League position
}

// ---------------------------------------------------------------------------
// Event Status (GET /event-status/)
// ---------------------------------------------------------------------------
//
// This is the "heartbeat" endpoint. The poller hits it to determine whether
// bonus points have been added and leagues have been updated — which signals
// that a gameweek is fully finalized and it's safe to run the stats engine.
// ---------------------------------------------------------------------------

// EventStatusResponse is the top-level response from /event-status/.
type EventStatusResponse struct {
	Status  []EventStatus `json:"status"`  // One entry per matchday (typically 3)
	Leagues string        `json:"leagues"` // "Updated" when league tables are current
}

// EventStatus represents the processing state for a single matchday.
type EventStatus struct {
	BonusAdded bool   `json:"bonus_added"` // Bonus points have been awarded
	Date       string `json:"date"`        // ISO date (YYYY-MM-DD)
	Event      int    `json:"event"`       // Gameweek number this status refers to
	Points     string `json:"points"`      // Processing status (e.g., "r" = raw/recalculated)
}

// ---------------------------------------------------------------------------
// H2H Standings (GET /leagues-h2h/{id}/standings/)
// ---------------------------------------------------------------------------
//
// This endpoint returns the head-to-head league table. It's paginated — each
// page has up to 50 entries. For our small league this fits in one page, but
// the client handles pagination for correctness.
//
// The response has three top-level keys: "league" (metadata), "standings"
// (the actual table), and "new_entries" (we don't need this).
// ---------------------------------------------------------------------------

// H2HStandingsResponse is the top-level response from /leagues-h2h/{id}/standings/.
type H2HStandingsResponse struct {
	League    LeagueInfo `json:"league"`
	Standings Standings  `json:"standings"`
}

// LeagueInfo contains metadata about the league.
type LeagueInfo struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Scoring string `json:"scoring"` // "h" for head-to-head
}

// Standings wraps the paginated list of league entries.
type Standings struct {
	HasNext bool            `json:"has_next"` // More pages available
	Page    int             `json:"page"`     // Current page number
	Results []StandingEntry `json:"results"`  // The actual standings rows
}

// StandingEntry represents one manager's row in the H2H league table.
type StandingEntry struct {
	ID            int    `json:"id"`              // Standing row ID (FPL internal)
	EntryID       int    `json:"entry"`           // Manager's entry/team ID
	PlayerName    string `json:"player_name"`     // Manager's real name
	EntryName     string `json:"entry_name"`      // Fantasy team name
	Rank          int    `json:"rank"`            // Current league rank
	LastRank      int    `json:"last_rank"`       // Rank after previous gameweek
	Total         int    `json:"total"`           // H2H points (W=3, D=1, L=0)
	MatchesPlayed int    `json:"matches_played"`
	MatchesWon    int    `json:"matches_won"`
	MatchesDrawn  int    `json:"matches_drawn"`
	MatchesLost   int    `json:"matches_lost"`
	PointsFor     int    `json:"points_for"` // Total FPL points scored across all H2H matches
}

// ---------------------------------------------------------------------------
// Manager History (GET /entry/{id}/history/)
// ---------------------------------------------------------------------------
//
// Returns a manager's performance across all gameweeks this season, plus
// any chips they've used. The "current" array grows by one entry each GW.
// The "chips" array lists each chip usage with the gameweek it was played.
// ---------------------------------------------------------------------------

// ManagerHistoryResponse is the top-level response from /entry/{id}/history/.
type ManagerHistoryResponse struct {
	Current []GameweekHistory `json:"current"` // One entry per completed gameweek
	Chips   []ChipUsage       `json:"chips"`   // Chips the manager has played
}

// GameweekHistory records a manager's performance in a single gameweek.
type GameweekHistory struct {
	Event              int `json:"event"`                // Gameweek number
	Points             int `json:"points"`               // Points scored this GW
	TotalPoints        int `json:"total_points"`         // Cumulative season points
	Rank               int `json:"rank"`                 // GW rank (out of all FPL managers)
	OverallRank        int `json:"overall_rank"`         // Season-long overall rank
	Bank               int `json:"bank"`                 // Money in bank (÷10 for actual £)
	Value              int `json:"value"`                // Squad value (÷10 for actual £)
	EventTransfers     int `json:"event_transfers"`      // Transfers made this GW
	EventTransfersCost int `json:"event_transfers_cost"` // Point deductions for extra transfers
	PointsOnBench      int `json:"points_on_bench"`      // Points left on the bench
}

// ChipUsage records when a manager played a chip.
type ChipUsage struct {
	Event int    `json:"event"` // Gameweek the chip was used
	Name  string `json:"name"`  // "bboost", "3xc", "freehit", "wildcard"
}

// ---------------------------------------------------------------------------
// Manager Picks (GET /entry/{id}/event/{gw}/picks/)
// ---------------------------------------------------------------------------
//
// Returns the manager's selected team for a specific gameweek: which players
// they picked, who they captained, and whether they used a chip.
//
// Go concept — POINTER FOR NULLABLE JSON:
//
// The active_chip field is null when no chip was used, and a string like
// "bboost" when one was. In Go, a plain string's zero value is "" (empty
// string), not nil. You can't distinguish "no chip" from "empty string."
//
// By using *string (a pointer to a string), we get:
//   - nil   → JSON was null (no chip used)
//   - &"bboost" → JSON was "bboost"
//
// This is the idiomatic Go way to represent nullable JSON fields. In Swift
// this would be Optional<String>, in Kotlin it would be String?.
// ---------------------------------------------------------------------------

// ManagerPicksResponse is the top-level response from /entry/{id}/event/{gw}/picks/.
type ManagerPicksResponse struct {
	ActiveChip    *string         `json:"active_chip"`    // nil = no chip, e.g., "bboost"
	Picks         []Pick          `json:"picks"`          // 15 players (11 starting + 4 bench)
	AutomaticSubs []AutoSub       `json:"automatic_subs"` // Auto-substitutions applied
	EntryHistory  GameweekHistory `json:"entry_history"`  // This GW's performance summary
}

// Pick represents a single player selection in a manager's team.
type Pick struct {
	Element       int  `json:"element"`         // Player ID
	Position      int  `json:"position"`        // Squad slot (1–15)
	Multiplier    int  `json:"multiplier"`      // 1 = normal, 2 = captain, 3 = triple captain
	IsCaptain     bool `json:"is_captain"`
	IsViceCaptain bool `json:"is_vice_captain"`
}

// AutoSub represents an automatic substitution made by the FPL system.
type AutoSub struct {
	ElementIn  int `json:"element_in"`  // Player ID subbed in
	ElementOut int `json:"element_out"` // Player ID subbed out
	Event      int `json:"event"`       // Gameweek number
}
