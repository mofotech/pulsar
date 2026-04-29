package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any pending SQL migrations in the migrations/ directory.
// Migrations are tracked in the schema_migrations table; each file is applied
// at most once, in lexicographic (version) order.
//
// Only the "-- +migrate Up" section of each file is executed.  The
// "-- +migrate Down" section is ignored at runtime.
//
// If the schema was previously bootstrapped via initdb (tables exist but
// schema_migrations does not), migrations whose objects already exist are
// recorded as applied without being re-executed.
func (db *DB) Migrate(ctx context.Context, log *zap.Logger) error {
	// Detect whether schema_migrations is being created for the first time.
	var tableExists bool
	err := db.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM pg_tables
			WHERE schemaname = 'public' AND tablename = 'schema_migrations'
		)`).Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}

	// Create the tracking table if needed.
	if _, err := db.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Load already-applied versions.
	rows, err := db.pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()

	// Collect migration files in sorted order.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}

		data, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		upSQL := extractUpSection(string(data))
		if strings.TrimSpace(upSQL) == "" {
			log.Warn("migration has empty Up section, skipping", zap.String("file", name))
			continue
		}

		log.Info("applying migration", zap.String("version", version))

		tx, err := db.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}

		_, execErr := tx.Exec(ctx, upSQL)
		if execErr != nil {
			_ = tx.Rollback(ctx)

			// If the database was bootstrapped via initdb the objects already
			// exist.  Treat duplicate_table (42P07) and duplicate_object (42710)
			// as "already applied" so we simply record the version and move on.
			var pgErr *pgconn.PgError
			if errors.As(execErr, &pgErr) &&
				(pgErr.Code == "42P07" || pgErr.Code == "42710") && !tableExists {
				log.Warn("migration objects already exist (initdb bootstrap), marking as applied",
					zap.String("version", version))
				if _, err := db.pool.Exec(ctx,
					`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`,
					version); err != nil {
					return fmt.Errorf("record bootstrapped %s: %w", name, err)
				}
				continue
			}

			return fmt.Errorf("execute %s: %w", name, execErr)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", name, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}

		log.Info("migration applied", zap.String("version", version))
	}

	return nil
}

// extractUpSection returns the SQL between "-- +migrate Up" and the next
// "-- +migrate" directive (typically "-- +migrate Down"), or the end of file.
func extractUpSection(content string) string {
	const upMarker = "-- +migrate Up"
	const downMarker = "-- +migrate Down"

	start := strings.Index(content, upMarker)
	if start == -1 {
		// No marker — treat entire file as the Up section.
		return content
	}
	start += len(upMarker)

	rest := content[start:]
	if end := strings.Index(rest, downMarker); end != -1 {
		rest = rest[:end]
	}
	// Also stop at any other "-- +migrate" directive.
	if end := strings.Index(rest, "-- +migrate "); end != -1 {
		rest = rest[:end]
	}
	return rest
}
