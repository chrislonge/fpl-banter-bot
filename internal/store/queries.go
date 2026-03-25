package store

// ---------------------------------------------------------------------------
// SQL query constants
//
// All SQL lives in this file, separate from the Go logic in store.go.
// This makes queries easy to find, review, and test independently.
//
// Go concept — const vs var for SQL strings:
//
// We use `const` because these strings never change at runtime. The Go
// compiler can inline const values and catches any accidental reassignment
// at compile time. Use `var` only when you need a mutable value or a type
// that isn't a basic literal (e.g., slices, maps).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Write queries (upserts)
//
// Every write uses INSERT ... ON CONFLICT to be idempotent. This means
// the poller can safely re-process a gameweek without creating duplicates.
//
// There are two ON CONFLICT strategies:
//
//   DO UPDATE — for data that FPL may recalculate (standings, scores,
//   manager names). The EXCLUDED pseudo-table refers to the row that
//   was proposed but conflicted. "SET name = EXCLUDED.name" means
//   "update the existing row's name to the new value we tried to insert."
//
//   DO NOTHING — for immutable facts (chip usage). Once we detect that
//   a manager played a wildcard in GW 5, that fact never changes.
// ---------------------------------------------------------------------------

const upsertLeague = `
	INSERT INTO leagues (id, name, league_type)
	VALUES ($1, $2, $3)
	ON CONFLICT (id) DO UPDATE SET
		name        = EXCLUDED.name,
		league_type = EXCLUDED.league_type
`

const upsertManager = `
	INSERT INTO managers (league_id, id, name, team_name)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (league_id, id) DO UPDATE SET
		name      = EXCLUDED.name,
		team_name = EXCLUDED.team_name
`

const upsertGameweekStanding = `
	INSERT INTO gameweek_standings (league_id, event_id, manager_id, rank, points, total_score)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (league_id, event_id, manager_id) DO UPDATE SET
		rank        = EXCLUDED.rank,
		points      = EXCLUDED.points,
		total_score = EXCLUDED.total_score
`

const upsertChipUsage = `
	INSERT INTO chip_usage (league_id, manager_id, event_id, chip)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (league_id, manager_id, event_id, chip) DO NOTHING
`

const upsertH2HResult = `
	INSERT INTO h2h_results (league_id, event_id, manager_1_id, manager_1_score, manager_2_id, manager_2_score)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (league_id, event_id, manager_1_id, manager_2_id) DO UPDATE SET
		manager_1_score = EXCLUDED.manager_1_score,
		manager_2_score = EXCLUDED.manager_2_score
`

const upsertSnapshotMeta = `
	INSERT INTO gameweek_snapshot_meta (league_id, event_id, source, standings_fidelity)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (league_id, event_id) DO UPDATE SET
		source             = EXCLUDED.source,
		standings_fidelity = EXCLUDED.standings_fidelity
`

const upsertGameweekManagerStat = `
	INSERT INTO gameweek_manager_stats (
		league_id, event_id, manager_id, points_on_bench,
		captain_element_id, captain_points, captain_multiplier
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (league_id, event_id, manager_id) DO UPDATE SET
		points_on_bench   = EXCLUDED.points_on_bench,
		captain_element_id = EXCLUDED.captain_element_id,
		captain_points     = EXCLUDED.captain_points,
		captain_multiplier = EXCLUDED.captain_multiplier
`

const upsertGameweekAward = `
	INSERT INTO gw_awards (
		league_id, event_id, award_key, manager_id,
		opponent_manager_id, player_element_id, metric_value
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (league_id, event_id, award_key) DO UPDATE SET
		manager_id           = EXCLUDED.manager_id,
		opponent_manager_id  = EXCLUDED.opponent_manager_id,
		player_element_id    = EXCLUDED.player_element_id,
		metric_value         = EXCLUDED.metric_value
`

const deleteGameweekAwardsForEvent = `
	DELETE FROM gw_awards
	WHERE league_id = $1 AND event_id = $2
`

// ---------------------------------------------------------------------------
// Read queries
// ---------------------------------------------------------------------------

const getLeague = `
	SELECT id, name, league_type, created_at
	FROM leagues
	WHERE id = $1
`

const getManagers = `
	SELECT league_id, id, name, team_name, created_at
	FROM managers
	WHERE league_id = $1
	ORDER BY id
`

const getStandings = `
	SELECT league_id, event_id, manager_id, rank, points, total_score, created_at
	FROM gameweek_standings
	WHERE league_id = $1 AND event_id = $2
	ORDER BY rank
`

const getChipUsage = `
	SELECT league_id, manager_id, event_id, chip, detected_at
	FROM chip_usage
	WHERE league_id = $1 AND event_id = $2
	ORDER BY manager_id, chip
`

const getH2HResults = `
	SELECT league_id, event_id, manager_1_id, manager_1_score, manager_2_id, manager_2_score, created_at
	FROM h2h_results
	WHERE league_id = $1 AND event_id = $2
	ORDER BY manager_1_id, manager_2_id
`

const getH2HResultsRange = `
	SELECT league_id, event_id, manager_1_id, manager_1_score, manager_2_id, manager_2_score, created_at
	FROM h2h_results
	WHERE league_id = $1 AND event_id BETWEEN $2 AND $3
	ORDER BY event_id, manager_1_id, manager_2_id
`

const getStoredEventIDs = `
	SELECT DISTINCT event_id
	FROM gameweek_standings
	WHERE league_id = $1
	ORDER BY event_id
`

const getSnapshotMeta = `
	SELECT league_id, event_id, source, standings_fidelity, created_at
	FROM gameweek_snapshot_meta
	WHERE league_id = $1 AND event_id = $2
`

const getGameweekManagerStats = `
	SELECT
		league_id, event_id, manager_id, points_on_bench,
		captain_element_id, captain_points, captain_multiplier, created_at
	FROM gameweek_manager_stats
	WHERE league_id = $1 AND event_id = $2
	ORDER BY manager_id
`

const getGameweekAwards = `
	SELECT
		league_id, event_id, award_key, manager_id,
		opponent_manager_id, player_element_id, metric_value, created_at
	FROM gw_awards
	WHERE league_id = $1 AND event_id = $2
	ORDER BY award_key
`

const getStoredManagerStatEventIDs = `
	SELECT DISTINCT event_id
	FROM gameweek_manager_stats
	WHERE league_id = $1
	ORDER BY event_id
`

const getStoredAwardEventIDs = `
	SELECT DISTINCT event_id
	FROM gw_awards
	WHERE league_id = $1
	ORDER BY event_id
`

// getLatestEventID returns the highest gameweek number stored for a league.
// COALESCE returns 0 if no rows exist yet — this way the first poll knows
// it's starting from scratch without the caller needing special nil handling.
const getLatestEventID = `
	SELECT COALESCE(MAX(event_id), 0)
	FROM gameweek_standings
	WHERE league_id = $1
`
