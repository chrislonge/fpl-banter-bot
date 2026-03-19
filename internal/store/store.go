package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a read query finds no matching rows.
//
// This is a sentinel error — a package-level variable that callers check
// with errors.Is(err, store.ErrNotFound). By defining our own error, we
// hide the pgx driver's pgx.ErrNoRows from callers. This is Dependency
// Inversion applied to errors: callers depend on the store's contract,
// not on the database driver.
//
// In Swift this would be an enum case; in Go, sentinel errors (or custom
// error types) serve the same purpose.
var ErrNotFound = errors.New("store: not found")

// Store defines the persistence contract for fpl-banter-bot.
//
// Go concept — IMPLICIT INTERFACE SATISFACTION:
//
// In Go, there is no "implements" keyword. A type satisfies an interface
// simply by having all the methods the interface declares. This is called
// structural typing (vs. nominal typing in Swift/Java). The compiler
// checks it — if PostgresStore is missing a method, code that assigns it
// to a Store variable won't compile.
//
// Why an interface? The poller and stats engine depend on Store, not on
// PostgresStore. This means:
//   - We could swap PostgresStore for an in-memory implementation in tests.
//   - The poller package never imports pgx — it only knows about Store.
//
// This is the Dependency Inversion Principle (the D in SOLID): high-level
// modules define the interface; low-level modules implement it.
type Store interface {
	// --- Writes (the poller calls these after each gameweek) ---

	UpsertLeague(ctx context.Context, league League) error
	UpsertManager(ctx context.Context, manager Manager) error
	UpsertGameweekStanding(ctx context.Context, standing GameweekStanding) error
	UpsertChipUsage(ctx context.Context, chip ChipUsage) error
	UpsertH2HResult(ctx context.Context, result H2HResult) error

	// SaveGameweekSnapshot writes all standings, chip usages, and H2H
	// results for a gameweek in a single database transaction. If any
	// write fails, the entire batch is rolled back — no partial data.
	//
	// Prerequisites: the league and all referenced managers must already
	// exist (upserted via UpsertLeague/UpsertManager before calling this).
	SaveGameweekSnapshot(ctx context.Context, standings []GameweekStanding, chips []ChipUsage, results []H2HResult) error

	// --- Reads (the stats engine calls these to diff gameweeks) ---

	GetStandings(ctx context.Context, leagueID int64, eventID int) ([]GameweekStanding, error)
	GetChipUsage(ctx context.Context, leagueID int64, eventID int) ([]ChipUsage, error)
	GetH2HResults(ctx context.Context, leagueID int64, eventID int) ([]H2HResult, error)
	GetManagers(ctx context.Context, leagueID int64) ([]Manager, error)
	GetLeague(ctx context.Context, leagueID int64) (League, error)
	GetLatestEventID(ctx context.Context, leagueID int64) (int, error)
}

// PostgresStore implements Store using a pgx connection pool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// Go concept — COMPILE-TIME INTERFACE CHECK:
//
// This line declares a package-level variable of type Store and assigns
// (*PostgresStore)(nil) to it. The nil pointer is never used — the only
// purpose is to make the compiler verify that *PostgresStore implements
// Store. If you add a method to Store and forget to implement it on
// PostgresStore, this line produces a compile error immediately.
//
// The underscore name (_) tells Go "I know this variable is unused."
// This is a widely-used Go idiom — you'll see it in the standard library
// and most popular open-source projects.
var _ Store = (*PostgresStore)(nil)

// New creates a PostgresStore backed by the given connection pool.
//
// The pool is injected rather than created here — the caller (main.go)
// owns the pool's lifecycle and passes it in. This follows the same
// Dependency Injection pattern used for the FPL client's http.Client.
func New(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// ---------------------------------------------------------------------------
// Write methods
// ---------------------------------------------------------------------------

func (s *PostgresStore) UpsertLeague(ctx context.Context, league League) error {
	// pool.Exec executes a query that doesn't return rows. It's the right
	// choice for INSERT, UPDATE, DELETE — any statement where you don't
	// need to scan results. (Compare: QueryRow for one row, Query for many.)
	_, err := s.pool.Exec(ctx, upsertLeague, league.ID, league.Name, league.Type)
	return err
}

func (s *PostgresStore) UpsertManager(ctx context.Context, manager Manager) error {
	_, err := s.pool.Exec(ctx, upsertManager, manager.LeagueID, manager.ID, manager.Name, manager.TeamName)
	return err
}

func (s *PostgresStore) UpsertGameweekStanding(ctx context.Context, standing GameweekStanding) error {
	_, err := s.pool.Exec(ctx, upsertGameweekStanding,
		standing.LeagueID, standing.EventID, standing.ManagerID,
		standing.Rank, standing.Points, standing.TotalScore,
	)
	return err
}

func (s *PostgresStore) UpsertChipUsage(ctx context.Context, chip ChipUsage) error {
	_, err := s.pool.Exec(ctx, upsertChipUsage, chip.LeagueID, chip.ManagerID, chip.EventID, chip.Chip)
	return err
}

func (s *PostgresStore) UpsertH2HResult(ctx context.Context, result H2HResult) error {
	_, err := s.pool.Exec(ctx, upsertH2HResult,
		result.LeagueID, result.EventID,
		result.Manager1ID, result.Manager1Score,
		result.Manager2ID, result.Manager2Score,
	)
	return err
}

// SaveGameweekSnapshot writes an entire gameweek's data atomically using
// a database transaction.
//
// Go concept — TRANSACTION PATTERN:
//
//	tx, err := pool.Begin(ctx)   // Start the transaction
//	defer tx.Rollback(ctx)       // Safety net: rollback if we return early
//	// ... do work with tx ...
//	return tx.Commit(ctx)        // Commit; deferred Rollback becomes a no-op
//
// The defer tx.Rollback(ctx) is a safety net. If any operation returns an
// error and we exit the function early, the deferred Rollback undoes all
// changes. If we reach tx.Commit(), the commit succeeds and the subsequent
// deferred Rollback is a no-op (rolling back an already-committed
// transaction does nothing). This pattern guarantees cleanup regardless
// of which code path executes.
func (s *PostgresStore) SaveGameweekSnapshot(ctx context.Context, standings []GameweekStanding, chips []ChipUsage, results []H2HResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			// Log but don't override the original error — rollback after
			// commit returns pgx.ErrTxClosed, which is expected.
		}
	}()

	for _, st := range standings {
		if _, err := tx.Exec(ctx, upsertGameweekStanding,
			st.LeagueID, st.EventID, st.ManagerID,
			st.Rank, st.Points, st.TotalScore,
		); err != nil {
			return fmt.Errorf("upsert standing for manager %d: %w", st.ManagerID, err)
		}
	}

	for _, ch := range chips {
		if _, err := tx.Exec(ctx, upsertChipUsage, ch.LeagueID, ch.ManagerID, ch.EventID, ch.Chip); err != nil {
			return fmt.Errorf("upsert chip for manager %d: %w", ch.ManagerID, err)
		}
	}

	for _, r := range results {
		if _, err := tx.Exec(ctx, upsertH2HResult,
			r.LeagueID, r.EventID,
			r.Manager1ID, r.Manager1Score,
			r.Manager2ID, r.Manager2Score,
		); err != nil {
			return fmt.Errorf("upsert h2h result (%d vs %d): %w", r.Manager1ID, r.Manager2ID, err)
		}
	}

	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Read methods
// ---------------------------------------------------------------------------

func (s *PostgresStore) GetLeague(ctx context.Context, leagueID int64) (League, error) {
	// QueryRow executes a query expected to return at most one row.
	// If no row matches, Scan returns pgx.ErrNoRows, which we translate
	// to our own ErrNotFound so callers don't depend on the pgx driver.
	var l League
	err := s.pool.QueryRow(ctx, getLeague, leagueID).Scan(&l.ID, &l.Name, &l.Type, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return League{}, ErrNotFound
	}
	return l, err
}

func (s *PostgresStore) GetManagers(ctx context.Context, leagueID int64) ([]Manager, error) {
	// Query returns a Rows iterator for multi-row results. The pattern:
	//   1. Call Query → get rows
	//   2. defer rows.Close() → prevents connection pool leaks
	//   3. Loop with rows.Next() → advances to the next row
	//   4. rows.Scan(&field) → copies column values into Go variables
	//   5. Check rows.Err() → catches errors during iteration (e.g.,
	//      network disconnect mid-stream). rows.Next() returns false on
	//      error, so without this check you'd silently get partial data.
	rows, err := s.pool.Query(ctx, getManagers, leagueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var managers []Manager
	for rows.Next() {
		var m Manager
		if err := rows.Scan(&m.LeagueID, &m.ID, &m.Name, &m.TeamName, &m.CreatedAt); err != nil {
			return nil, err
		}
		managers = append(managers, m)
	}
	return managers, rows.Err()
}

func (s *PostgresStore) GetStandings(ctx context.Context, leagueID int64, eventID int) ([]GameweekStanding, error) {
	rows, err := s.pool.Query(ctx, getStandings, leagueID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var standings []GameweekStanding
	for rows.Next() {
		var st GameweekStanding
		if err := rows.Scan(&st.LeagueID, &st.EventID, &st.ManagerID, &st.Rank, &st.Points, &st.TotalScore, &st.CreatedAt); err != nil {
			return nil, err
		}
		standings = append(standings, st)
	}
	return standings, rows.Err()
}

func (s *PostgresStore) GetChipUsage(ctx context.Context, leagueID int64, eventID int) ([]ChipUsage, error) {
	rows, err := s.pool.Query(ctx, getChipUsage, leagueID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chips []ChipUsage
	for rows.Next() {
		var c ChipUsage
		if err := rows.Scan(&c.LeagueID, &c.ManagerID, &c.EventID, &c.Chip, &c.DetectedAt); err != nil {
			return nil, err
		}
		chips = append(chips, c)
	}
	return chips, rows.Err()
}

func (s *PostgresStore) GetH2HResults(ctx context.Context, leagueID int64, eventID int) ([]H2HResult, error) {
	rows, err := s.pool.Query(ctx, getH2HResults, leagueID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []H2HResult
	for rows.Next() {
		var r H2HResult
		if err := rows.Scan(&r.LeagueID, &r.EventID, &r.Manager1ID, &r.Manager1Score, &r.Manager2ID, &r.Manager2Score, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetLatestEventID returns the highest gameweek number stored for a league.
// Returns 0 if no data exists yet (the SQL uses COALESCE(MAX(...), 0)).
func (s *PostgresStore) GetLatestEventID(ctx context.Context, leagueID int64) (int, error) {
	var eventID int
	err := s.pool.QueryRow(ctx, getLatestEventID, leagueID).Scan(&eventID)
	return eventID, err
}
