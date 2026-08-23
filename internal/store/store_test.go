package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesIdempotentWALSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalogue.db")
	db, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal mode = %s", mode)
	}
	for _, table := range []string{"organizations", "repositories", "skills", "skill_revisions", "audit_events", "embedding_cache", "skill_feedback"} {
		var n string
		if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
	db.Close()
	db, err = Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	var notNull int
	if err := db.QueryRow("SELECT \"notnull\" FROM pragma_table_info('skill_revisions') WHERE name='version'").Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 0 {
		t.Fatalf("version column notnull = %d, want nullable", notNull)
	}
}

func TestOpenRejectsMigrationHistoryGaps(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalogue.db")
	seedMigrationHistory(t, p, []int{1, 3})

	_, err := Open(context.Background(), p)
	if err == nil {
		t.Fatal("Open accepted a migration history with a gap")
	}
	if !strings.Contains(err.Error(), "migration history gap") {
		t.Fatalf("error = %q, want migration history gap", err)
	}
}

func TestOpenRejectsPartiallyRecordedMigration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalogue.db")
	seedMigrationHistory(t, p, []int{1, 2})

	_, err := Open(context.Background(), p)
	if err == nil {
		t.Fatal("Open accepted a partially applied migration")
	}
	if !strings.Contains(err.Error(), "migration 2 schema incomplete") {
		t.Fatalf("error = %q, want migration 2 schema incomplete", err)
	}
}

func TestOpenRejectsUnrecordedPartialMigration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalogue.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema + `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP); INSERT INTO schema_migrations(version) VALUES (1); ALTER TABLE repositories ADD COLUMN poll_interval TEXT NOT NULL DEFAULT '15m';`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(context.Background(), p)
	if err == nil {
		t.Fatal("Open accepted an unrecorded partial migration")
	}
	if !strings.Contains(err.Error(), "migration 1 schema contains later-version objects") {
		t.Fatalf("error = %q, want unrecorded partial migration error", err)
	}
}

func TestOpenRejectsRecordedFeedbackMigrationWithoutTable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalogue.db")
	db, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE skill_feedback`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(context.Background(), p)
	if err == nil {
		t.Fatal("Open accepted migration 8 without skill_feedback")
	}
	if !strings.Contains(err.Error(), "migration 8 schema incomplete") {
		t.Fatalf("error = %q, want migration 8 schema incomplete", err)
	}
}

func TestOpenConfiguresForeignKeysOnEveryConnection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalogue.db")
	db, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)

	first, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for name, conn := range map[string]*sql.Conn{"first": first, "second": second} {
		var enabled int
		if err := conn.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&enabled); err != nil {
			t.Fatalf("%s connection: %v", name, err)
		}
		if enabled != 1 {
			t.Errorf("%s connection foreign_keys = %d, want 1", name, enabled)
		}
	}
}

func seedMigrationHistory(t *testing.T, path string, versions []int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema + `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		if _, err := db.Exec("INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
			t.Fatalf("insert migration %d: %v", version, err)
		}
	}
}

var _ *sql.DB
