// SPDX-License-Identifier: AGPL-3.0-or-later

// Package db opens the SQLite database and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"

	"imvault/internal/closer"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (creating if needed) the SQLite database at path and brings the
// schema up to date. A database a newer Imvault has migrated is refused with a
// *NewerSchemaError before anything is applied.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	sqlDB, err := connect(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, sqlDB); err != nil {
		return nil, errors.Join(withPath(err, path), sqlDB.Close())
	}
	return sqlDB, nil
}

func connect(ctx context.Context, path string) (*sql.DB, error) {
	sqlDB, err := sql.Open("sqlite", dsn(path))

	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite allows one writer at a time. Serialising access through a single
	// connection avoids SQLITE_BUSY entirely and is plenty for this workload.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("ping sqlite: %w", err), sqlDB.Close())
	}
	return sqlDB, nil
}

// OpenCurrent opens an existing database without changing its schema, and
// only when the schema is exactly the one this binary was built for.
//
// It is for commands that read the database to produce something meant to
// outlive it, which today is backup. Migrating first would make a backup taken
// before an upgrade a copy of the upgraded schema, the one thing it must not
// be; and a database a newer Imvault has migrated may hold rows this binary
// cannot see, so a backup of it would verify against what it understands and
// still be incomplete. Either way the right binary is the one whose schema the
// database already has.
func OpenCurrent(ctx context.Context, path string) (*sql.DB, error) {
	sqlDB, err := connect(ctx, path)
	if err != nil {
		return nil, err
	}
	pending, err := Pending(ctx, sqlDB)
	if err == nil && len(pending) != 0 {
		err = &OlderSchemaError{Pending: pending}
	}
	if err != nil {
		return nil, errors.Join(withPath(err, path), sqlDB.Close())
	}
	return sqlDB, nil
}

// NewerSchemaError reports a database carrying migrations this binary does not
// have, which means a newer Imvault has already upgraded it.
//
// Migrations only run forwards. An older binary would read and write tables
// whose meaning has changed underneath it, and nothing would say so until the
// damage showed, so every command that opens the database refuses instead.
type NewerSchemaError struct {
	Path    string
	Unknown []string
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("%s was upgraded by a newer Imvault: it has %d migration(s) this binary does not know (%s). "+
		"This is an older Imvault binary running against a newer database, and it will not open it. "+
		"Install the newer Imvault again, or restore the backup taken before the upgrade",
		describe(e.Path), len(e.Unknown), strings.Join(e.Unknown, ", "))
}

// OlderSchemaError reports migrations this binary would apply, to a command
// that must not apply them.
type OlderSchemaError struct {
	Path    string
	Pending []string
}

func (e *OlderSchemaError) Error() string {
	return fmt.Sprintf("%s predates this Imvault binary: %d migration(s) have not been applied (%s). "+
		"This command does not migrate the database. Run it with the Imvault version that last used this database, "+
		"or start this version's server once to migrate the database and run it afterwards",
		describe(e.Path), len(e.Pending), strings.Join(e.Pending, ", "))
}

func describe(path string) string {
	if path == "" {
		return "the database"
	}
	return "database " + path
}

// withPath names the database in a schema error, which is created before the
// path is known to the code that detects it.
func withPath(err error, path string) error {
	var newer *NewerSchemaError
	if errors.As(err, &newer) {
		newer.Path = path
	}
	var older *OlderSchemaError
	if errors.As(err, &older) {
		older.Path = path
	}
	return err
}

// embeddedMigrations lists the migrations embedded in this binary, in the order they run.
func embeddedMigrations() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Pending reports which embedded migrations the database has not applied, and
// returns a *NewerSchemaError when it has applied any this binary lacks. It
// only reads, so it is safe on a read-only or immutable snapshot. A database
// with no schema_migrations table has applied nothing.
func Pending(ctx context.Context, q Queryer) ([]string, error) {
	var tables int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&tables); err != nil {
		return nil, fmt.Errorf("look for schema_migrations: %w", err)
	}
	applied := map[string]bool{}
	if tables != 0 {
		var err error
		if applied, err = appliedMigrations(ctx, q); err != nil {
			return nil, err
		}
	}
	return pendingAgainst(applied)
}

// Queryer is the read side of *sql.DB and *sql.Tx.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// pendingAgainst compares applied migrations with the embedded ones. Unknown
// applied migrations are an error whatever else is pending: an older binary
// must not apply even its own migrations to a newer schema.
func pendingAgainst(applied map[string]bool) ([]string, error) {
	names, err := embeddedMigrations()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(names))
	var pending []string
	for _, name := range names {
		known[name] = true
		if !applied[name] {
			pending = append(pending, name)
		}
	}
	var unknown []string
	for name := range applied {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) != 0 {
		sort.Strings(unknown)
		return nil, &NewerSchemaError{Unknown: unknown}
	}
	return pending, nil
}

// dsn builds the connection URI for a database file.
//
// The path is escaped, because SQLite reads it as a URI: a directory name
// containing "?" or "#" would otherwise end the path early. Transactions start
// IMMEDIATE, taking the write lock up front. Within one process the single
// connection already serialises writers, but a maintenance command can share
// the database with the server, and a deferred transaction that reads and then
// writes would fail outright rather than wait for the busy timeout.
func dsn(path string) string {
	return "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
}

func migrate(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedMigrations(ctx, sqlDB)
	if err != nil {
		return err
	}
	pending, err := pendingAgainst(applied)
	if err != nil {
		return err
	}
	for _, name := range pending {
		if err := applyMigration(ctx, sqlDB, name); err != nil {
			return err
		}
	}
	return nil
}

// appliedMigrations reads which migrations have already run. The rows are
// closed before returning, since the single connection is needed next.
func appliedMigrations(ctx context.Context, q Queryer) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer closer.Discard(rows)
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// applyMigration commits both the schema change and its version atomically.
func applyMigration(ctx context.Context, sqlDB *sql.DB, name string) error {
	body, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return errors.Join(fmt.Errorf("apply migration %s: %w", name, err), tx.Rollback())
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		name, time.Now().UTC().Unix(),
	); err != nil {
		return errors.Join(fmt.Errorf("record migration %s: %w", name, err), tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}
