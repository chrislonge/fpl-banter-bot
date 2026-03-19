package store

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// Go concept — BLANK IMPORT (underscore import):
	//
	// This import has no name — just an underscore. That means we never
	// call any function from this package directly. So why import it?
	//
	// In Go, every package can have an init() function that runs
	// automatically when the package is loaded. The pgx/v5 database
	// driver registers itself with golang-migrate during its init().
	// Without this import, golang-migrate wouldn't know how to talk to
	// Postgres via pgx.
	//
	// This pattern is called "importing for side effects" and is common
	// for database drivers, image decoders, and other plugin-style packages.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
)

// Go concept — embed.FS (embedded filesystem):
//
// The //go:embed directive tells the Go compiler to read the contents
// of the "migrations" directory AT COMPILE TIME and bake them into the
// binary as an in-memory filesystem. This means:
//
//   - Migration SQL files ship INSIDE the executable — no separate files
//     to deploy or lose on the Raspberry Pi.
//   - The embed.FS implements io/fs.FS, so any code that reads files
//     through that interface works transparently (including golang-migrate).
//
// The directive MUST appear immediately before the variable it populates.
// It's a compiler directive, not a comment — removing it breaks the build.

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies all pending database migrations using the SQL
// files embedded in the binary. It's called once at startup before the
// store is used.
//
// The databaseURL parameter should be a standard Postgres connection
// string (e.g., "postgres://user:pass@host:5432/dbname?sslmode=disable").
func RunMigrations(databaseURL string) (err error) {
	// golang-migrate's pgx5 driver registers itself under the "pgx5"
	// scheme, not "postgres" or "postgresql". We swap the scheme prefix
	// so callers can use standard Postgres URLs everywhere.
	migrateURL := toPgx5Scheme(databaseURL)

	// iofs.New wraps our embedded filesystem so golang-migrate can read
	// migration files from it. The second argument is the subdirectory
	// within the embed.FS that contains the .sql files.
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL)
	if err != nil {
		return err
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			err = errors.Join(err, fmt.Errorf("close migration source: %w", srcErr))
		}
		if dbErr != nil {
			err = errors.Join(err, fmt.Errorf("close migration db: %w", dbErr))
		}
	}()

	// Run all pending migrations. migrate.ErrNoChange means all
	// migrations are already applied — that's success, not an error.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}

// toPgx5Scheme replaces the "postgres://" or "postgresql://" URL scheme
// with "pgx5://" so golang-migrate routes to the pgx5 database driver.
func toPgx5Scheme(databaseURL string) string {
	switch {
	case strings.HasPrefix(databaseURL, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgresql://")
	case strings.HasPrefix(databaseURL, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgres://")
	default:
		return databaseURL
	}
}
