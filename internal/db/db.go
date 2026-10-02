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
// schema up to date.
func Open(ctx context.Context, path string) (*sql.DB, error) {
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

	if err := migrate(ctx, sqlDB); err != nil {
		return nil, errors.Join(err, sqlDB.Close())
	}

	return sqlDB, nil
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

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		if err := applyMigration(ctx, sqlDB, name); err != nil {
			return err
		}
	}
	return nil
}

// appliedMigrations reads which migrations have already run. The rows are
// closed before returning, since the single connection is needed next.
func appliedMigrations(ctx context.Context, sqlDB *sql.DB) (map[string]bool, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
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
