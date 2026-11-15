package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
)

// migrationLockKey is the advisory-lock id that serialises concurrent migrators.
const migrationLockKey = 727274001

// Migrate applies every *.sql file in fsys not yet recorded in schema_migrations.
// It holds a session advisory lock so concurrent runs queue instead of racing.
// It returns the names of the files applied by this call.
func Migrate(ctx context.Context, conn *pgx.Conn, fsys fs.FS) ([]string, error) {
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return nil, fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		// Best effort: closing the session also drops the lock.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	// Default privileges would hand the app role DML on this bookkeeping table (PRD 6.4: orders and seq_log only).
	if _, err := conn.Exec(ctx, `DO $$ BEGIN
		IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
			REVOKE ALL ON schema_migrations FROM app;
		END IF; END $$`); err != nil {
		return nil, fmt.Errorf("revoke app on schema_migrations: %w", err)
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)

	var applied []string
	for _, name := range names {
		done, err := applyOne(ctx, conn, fsys, name)
		if err != nil {
			return applied, err
		}
		if done {
			applied = append(applied, name)
		}
	}
	return applied, nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, fsys fs.FS, name string) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name=$1)", name).Scan(&exists); err != nil {
		return false, fmt.Errorf("check %s: %w", name, err)
	}
	if exists {
		return false, nil
	}
	sqlBytes, err := fs.ReadFile(fsys, name)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// No arguments => simple protocol, so a file may hold several statements.
	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		return false, fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (name) VALUES ($1)", name); err != nil {
		return false, fmt.Errorf("record %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit %s: %w", name, err)
	}
	return true, nil
}
