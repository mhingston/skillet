package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `CREATE TABLE organizations (id TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '');
CREATE TABLE repositories (id TEXT PRIMARY KEY, organization_id TEXT NOT NULL REFERENCES organizations(id), url TEXT NOT NULL, tracked_ref TEXT NOT NULL, trust_level TEXT NOT NULL, owner TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE skills (id TEXT PRIMARY KEY, organization_id TEXT NOT NULL REFERENCES organizations(id), repository_id TEXT NOT NULL REFERENCES repositories(id), relative_path TEXT NOT NULL, active_revision_id TEXT, searchable INTEGER NOT NULL DEFAULT 0);
CREATE TABLE skill_revisions (id TEXT PRIMARY KEY, skill_id TEXT NOT NULL REFERENCES skills(id), commit_sha TEXT NOT NULL, tree_sha TEXT NOT NULL DEFAULT '', archive_sha256_tar_gz TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, validation_result_json TEXT NOT NULL DEFAULT '{}', license TEXT NOT NULL DEFAULT '', compatibility TEXT NOT NULL DEFAULT '', allowed_tools TEXT NOT NULL DEFAULT '');
CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, organization_id TEXT NOT NULL REFERENCES organizations(id), actor_type TEXT NOT NULL DEFAULT '', actor_id TEXT NOT NULL DEFAULT '', event_type TEXT NOT NULL, repository_id TEXT NOT NULL DEFAULT '', skill_id TEXT NOT NULL DEFAULT '', revision_id TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', details_json TEXT NOT NULL DEFAULT '{}', occurred_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`

func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=foreign_keys(1)"
	} else {
		dsn += "&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("begin migration: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("create migration table: %w", err)
	}
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("read migration version: %w", err)
	}
	if version > 8 {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("unsupported schema migration version %d", version)
	}
	if err := validateMigrationHistory(ctx, tx, version); err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	if version == 1 {
		for _, column := range migrationTwoColumns {
			if has, err := tableHasColumn(ctx, tx, column.table, column.name); err != nil {
				tx.Rollback()
				db.Close()
				return nil, err
			} else if has {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("migration 1 schema contains later-version objects")
			}
		}
	}
	if version >= 2 {
		for _, column := range migrationTwoColumns {
			has, err := tableHasColumn(ctx, tx, column.table, column.name)
			if err != nil {
				tx.Rollback()
				db.Close()
				return nil, err
			}
			if !has {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("migration 2 schema incomplete")
			}
		}
	}
	if version >= 3 {
		has, err := tableHasColumn(ctx, tx, "skill_revisions", "has_scripts")
		if err != nil || !has {
			tx.Rollback()
			db.Close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("migration 3 schema incomplete")
		}
	}
	if version >= 4 {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='embedding_cache'").Scan(&exists); err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if exists != 1 {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("migration 4 schema incomplete")
		}
	}
	if version >= 5 {
		for _, column := range migrationFiveColumns {
			has, err := tableHasColumn(ctx, tx, column.table, column.name)
			if err != nil || !has {
				tx.Rollback()
				db.Close()
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("migration 5 schema incomplete")
			}
		}
	}
	if version >= 6 {
		for _, column := range migrationSixColumns {
			has, err := tableHasColumn(ctx, tx, column.table, column.name)
			if err != nil || !has {
				tx.Rollback()
				db.Close()
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("migration 6 schema incomplete")
			}
		}
	}
	if version >= 7 {
		has, err := tableHasColumn(ctx, tx, "skill_revisions", "version")
		if err != nil || !has {
			tx.Rollback()
			db.Close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("migration 7 schema incomplete")
		}
	}
	if version >= 8 {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='skill_feedback'").Scan(&exists); err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if exists != 1 {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("migration 8 schema incomplete")
		}
	}
	if version == 0 {
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("apply migration 1: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (1)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration: %w", err)
		}
		version = 1
	}
	if version < 2 {
		for _, statement := range []string{
			`ALTER TABLE repositories ADD COLUMN poll_interval TEXT NOT NULL DEFAULT '15m'`,
			`ALTER TABLE repositories ADD COLUMN last_seen_commit TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE repositories ADD COLUMN last_successful_sync_at TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE repositories ADD COLUMN last_failed_sync_at TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE repositories ADD COLUMN last_failure TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE skills ADD COLUMN name TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE skills ADD COLUMN owner TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE skill_revisions ADD COLUMN name TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE skill_revisions ADD COLUMN description TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE skill_revisions ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}'`,
			`ALTER TABLE skill_revisions ADD COLUMN archive_sha256_zip TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE skill_revisions ADD COLUMN admitted_at TEXT NOT NULL DEFAULT ''`,
		} {
			if version == 1 {
				if _, err = tx.ExecContext(ctx, statement); err != nil {
					tx.Rollback()
					db.Close()
					return nil, fmt.Errorf("apply migration 2: %w", err)
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (2)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 2: %w", err)
		}
		version = 2
	}
	if version < 3 {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE skill_revisions ADD COLUMN has_scripts INTEGER NOT NULL DEFAULT 0`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("apply migration 3: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (3)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 3: %w", err)
		}
		version = 3
	}
	if version < 4 {
		if _, err = tx.ExecContext(ctx, `CREATE TABLE embedding_cache (provider TEXT NOT NULL, model TEXT NOT NULL, dimensions INTEGER NOT NULL, routing_document_digest TEXT NOT NULL, vector BLOB NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(provider, model, dimensions, routing_document_digest))`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("apply migration 4: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (4)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 4: %w", err)
		}
		version = 4
	}
	if version < 5 {
		for _, column := range migrationFiveColumns {
			has, checkErr := tableHasColumn(ctx, tx, column.table, column.name)
			if checkErr != nil {
				tx.Rollback()
				db.Close()
				return nil, checkErr
			}
			if has {
				continue
			}
			statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT NOT NULL DEFAULT ''", column.table, column.name)
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("apply migration 5: %w", err)
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (5)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 5: %w", err)
		}
		version = 5
	}
	if version < 6 {
		for _, column := range migrationSixColumns {
			has, checkErr := tableHasColumn(ctx, tx, column.table, column.name)
			if checkErr != nil {
				tx.Rollback()
				db.Close()
				return nil, checkErr
			}
			if has {
				continue
			}
			statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT NOT NULL DEFAULT ''", column.table, column.name)
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("apply migration 6: %w", err)
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (6)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 6: %w", err)
		}
		version = 6
	}
	if version < 7 {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE skill_revisions ADD COLUMN version TEXT DEFAULT NULL`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("apply migration 7: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (7)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 7: %w", err)
		}
		version = 7
	}
	if version < 8 {
		if _, err = tx.ExecContext(ctx, `CREATE TABLE skill_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			skill_id TEXT NOT NULL REFERENCES skills(id),
			revision_id TEXT NOT NULL REFERENCES skill_revisions(id),
			archive_sha256 TEXT NOT NULL,
			materialization_id TEXT NOT NULL,
			category TEXT NOT NULL,
			summary TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("apply migration 8: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (8)"); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record migration 8: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, fmt.Errorf("commit migration: %w", err)
	}
	return db, nil
}

type migrationColumn struct{ table, name string }

var migrationTwoColumns = []migrationColumn{
	{"repositories", "poll_interval"}, {"repositories", "last_seen_commit"},
	{"repositories", "last_successful_sync_at"}, {"repositories", "last_failed_sync_at"},
	{"repositories", "last_failure"}, {"skills", "name"}, {"skills", "owner"},
	{"skill_revisions", "name"}, {"skill_revisions", "description"},
	{"skill_revisions", "metadata_json"}, {"skill_revisions", "archive_sha256_zip"},
	{"skill_revisions", "admitted_at"},
}

var migrationFiveColumns = []migrationColumn{
	{"audit_events", "actor_type"}, {"audit_events", "actor_id"},
	{"audit_events", "repository_id"}, {"audit_events", "skill_id"}, {"audit_events", "revision_id"},
}

var migrationSixColumns = []migrationColumn{
	{"skill_revisions", "license"}, {"skill_revisions", "compatibility"}, {"skill_revisions", "allowed_tools"},
}

func validateMigrationHistory(ctx context.Context, tx *sql.Tx, version int) error {
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()
	expected := 1
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			return err
		}
		if got != expected {
			return fmt.Errorf("migration history gap: expected %d, found %d", expected, got)
		}
		expected++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if expected-1 != version {
		return fmt.Errorf("migration history does not match recorded version")
	}
	return nil
}

func tableHasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect schema: %w", err)
	}
	return count > 0, nil
}
