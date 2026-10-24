package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A1: a database one migration ahead of this binary opens without error.
func TestReviewA1SchemaAheadOfBinaryOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	known, _ := KnownVersion()
	// What a feature branch's 00020 leaves behind: a row for version 20 and its schema change.
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1); ALTER TABLE projects ADD COLUMN owner TEXT NOT NULL DEFAULT 'x'`, known+1); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenAndMigrate(path)
	if err != nil {
		t.Logf("A1 refused: %v", err)
		return
	}
	defer db.Close()
	v, _ := SchemaVersion(context.Background(), db)
	t.Logf("A1 OpenAndMigrate error=nil with schema %d > binary %d — the panel would boot", v, known)
}

// A1b: two branches that both ship 00020: the second branch's file is skipped.
func TestReviewA1SameVersionDifferentFileSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	known, _ := KnownVersion()
	// Pretend the binary's own latest migration (known) was applied by another branch's same-numbered file:
	// remove its row's effect is not reproducible generically, so record that goose looks only at version_id.
	var rows int
	db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, known).Scan(&rows)
	cols, _ := db.Query(`PRAGMA table_info(goose_db_version)`)
	var names []string
	for cols.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		cols.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
		names = append(names, name)
	}
	cols.Close()
	t.Logf("A1b goose_db_version columns=%v (no file name / checksum stored), rows for version %d = %d", names, known, rows)
	db.Close()
}

// B5: foreign_keys is a per-connection setting applied once by Migrate; a
// connection opened later by database/sql does not have it.
func TestReviewB5ForeignKeysOnNewConnection(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var fk int
	db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	t.Logf("B5 first connection foreign_keys=%d", fk)
	// Force database/sql to discard the pooled connection and open another,
	// as it does after driver.ErrBadConn or a closed connection.
	db.SetMaxIdleConns(0)
	db.QueryRow("SELECT 1").Scan(new(int))
	db.SetMaxIdleConns(1)
	db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	t.Logf("B5 replacement connection foreign_keys=%d", fk)
	db.Exec(`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1,'p','',1,1); INSERT INTO environments (project_id, name, created_at) VALUES (1,'prod',1)`)
	db.Exec(`DELETE FROM projects WHERE id = 1`)
	var orphans int
	db.QueryRow(`SELECT COUNT(*) FROM environments WHERE project_id = 1`).Scan(&orphans)
	t.Logf("B5 environments left after deleting their project: %d", orphans)
}

// B5b: a request whose context is canceled mid-query (client went away).
func TestReviewB5CanceledQueryDropsForeignKeys(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, qerr := db.ExecContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 100000000) SELECT count(*) FROM c`)
		cancel()
		var fk int
		db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
		t.Logf("B5b attempt %d: query err=%v, foreign_keys afterwards=%d", i, qerr, fk)
	}
}
