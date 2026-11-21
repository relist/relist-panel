package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"relist-panel/internal/migrations"
)

func TestOpenAndMigrateCreatesDatabaseAndRunsEmbeddedMigrations(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "nested", "panel.db")
	db, err := OpenAndMigrate(databasePath)
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version != 43 {
		t.Fatalf("migration version = %d, want 43", version)
	}

	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query migrated database: %v", err)
	}
}

func TestSnapshotMigrationPreservesLegacyQueueAndCascadesPrivateInputs(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 16); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'p', '', 1, 1);
		INSERT INTO environments (id, project_id, name, created_at) VALUES (1, 1, 'production', 1);
		INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 1, 'web', 'services: {}', 1, 1);
		INSERT INTO deployments (id, compose_id, number, mode, no_cache, fresh_volumes, status, queued_at) VALUES (1, 1, 7, 'build', 1, 1, 'queued', 42);
	`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE id = 1 AND number = 7 AND mode = 'build' AND no_cache = 1 AND fresh_volumes = 1 AND status = 'queued' AND queued_at = 42`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("legacy queued row preserved = %d, error = %v", retained, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployment_snapshots`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("migration invented old queue inputs: count = %d, error = %v", retained, err)
	}
	if _, err := db.Exec(`
		INSERT INTO deployment_snapshots (deployment_id, snapshot) VALUES (1, '{"Version":1}');
		INSERT INTO compose_source_materializations (compose_id, source_dir) VALUES (1, '/private/source/src');
		DELETE FROM deployments WHERE id = 1;
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployment_snapshots`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("private input remained after history deletion: count = %d, error = %v", retained, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM compose_source_materializations`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("history pruning removed the last-built Source pointer: count = %d, error = %v", retained, err)
	}
	if _, err := db.Exec(`DELETE FROM composes WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM compose_source_materializations`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("private pointer remained after Compose deletion: count = %d, error = %v", retained, err)
	}
}

// A second process's writes must not fail outright when the panel already
// holds the write lock: the busy timeout makes OpenAndMigrate wait, so the
// reset-2fa command works against a running panel instead of returning
// "database is locked".
func TestOpenAndMigrateWaitsForABusyWriter(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "panel.db")

	writer, err := OpenAndMigrate(databasePath)
	if err != nil {
		t.Fatalf("OpenAndMigrate(writer) error = %v", err)
	}
	defer writer.Close()

	reader, err := OpenAndMigrate(databasePath)
	if err != nil {
		t.Fatalf("OpenAndMigrate(reader) error = %v", err)
	}
	defer reader.Close()

	locked := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		if _, err := writer.Exec("BEGIN IMMEDIATE"); err != nil {
			writerDone <- fmt.Errorf("begin immediate: %w", err)
			return
		}
		if _, err := writer.Exec(`
			INSERT INTO users (id, email, password_hash, created_at)
			VALUES (1, 'admin@example.com', 'hash', 1)
		`); err != nil {
			writer.Exec("ROLLBACK")
			writerDone <- fmt.Errorf("insert admin: %w", err)
			return
		}
		close(locked)
		time.Sleep(300 * time.Millisecond)
		_, err := writer.Exec("COMMIT")
		writerDone <- err
	}()

	<-locked

	if _, err := reader.Exec("UPDATE users SET failed_attempts = 1 WHERE id = 1"); err != nil {
		t.Fatalf("update while a writer holds the lock: %v", err)
	}
	if err := <-writerDone; err != nil {
		t.Fatalf("writer: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
}

// A Compose that existed before the deployed_at column (migration 00009) must
// read as never deployed afterwards: the column backfills to NULL, not to a
// defaulted timestamp.
func TestDeployTimestampsMigrationBackfillsExistingRowsToNull(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("configure goose SQLite dialect: %v", err)
	}
	if err := goose.UpTo(db, ".", 8); err != nil {
		t.Fatalf("migrate to version 8: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO projects (name, description, created_at, updated_at) VALUES ('Legacy', '', 1, 1);
		INSERT INTO environments (project_id, name, created_at) VALUES (1, 'production', 1);
		INSERT INTO composes (environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at)
			VALUES (1, 'web', 'legacy', 'production', 'services: {}', 1, 1);
	`); err != nil {
		t.Fatalf("seed pre-migration compose row: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	var deployedAt sql.NullInt64
	if err := db.QueryRow("SELECT deployed_at FROM composes WHERE id = 1").Scan(&deployedAt); err != nil {
		t.Fatalf("read deployed_at: %v", err)
	}
	if deployedAt.Valid {
		t.Fatalf("deployed_at = %+v, want NULL (never deployed stays never deployed)", deployedAt)
	}

	var version int
	if err := db.QueryRow("SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	// Later migrations exist independently of this one; the backfill only
	// needs 00009 itself to have been applied on the way to latest.
	if version < 9 {
		t.Fatalf("migration version = %d, want at least 9", version)
	}
}

// The scoped-variables migration must apply on a database that already holds
// composes at version 13, keeping them and adding the redeploy marker.
func TestScopedVariablesMigrationUpgradesVersion13(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 13); err != nil {
		t.Fatalf("migrate to 13: %v", err)
	}
	now := time.Now().Unix()
	if _, err := db.Exec("INSERT INTO projects (name, description, created_at, updated_at) VALUES ('p', '', ?, ?)", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO environments (project_id, name, created_at) VALUES (1, 'production', ?)", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'web', '', ?, ?)", now, now); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate 13 -> latest: %v", err)
	}
	var changedAt sql.NullInt64
	if err := db.QueryRow("SELECT variables_changed_at FROM composes WHERE id = 1").Scan(&changedAt); err != nil {
		t.Fatalf("existing compose lost or column missing: %v", err)
	}
	if changedAt.Valid {
		t.Fatalf("variables_changed_at = %d, want NULL", changedAt.Int64)
	}
	for _, table := range []string{"compose_variables", "variable_sets", "variable_set_entries", "service_bindings"} {
		if _, err := db.Exec("SELECT 1 FROM " + table); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}

	// Sets belong to a Compose: unique per Compose, removed with it.
	for _, statement := range []string{
		"INSERT INTO variable_sets (compose_id, name, created_at, updated_at) VALUES (1, 's', 0, 0)",
		"INSERT INTO variable_set_entries (set_id, key, value) VALUES (1, 'K', 'v')",
		"INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (1, 'api', 1, 0)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := db.Exec("INSERT INTO variable_sets (compose_id, name, created_at, updated_at) VALUES (1, 's', 0, 0)"); err == nil {
		t.Error("duplicate set name in one compose was accepted")
	}
	if _, err := db.Exec("DELETE FROM composes WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"variable_sets", "variable_set_entries", "service_bindings"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s rows after compose delete = %d (%v)", table, count, err)
		}
	}
}

// A database that applied 00014 holds Environment-owned sets. 00015 must move
// them to the Compose without the cascade from dropping the old table taking
// entries or bindings with it: every Compose of the Environment gets its own
// copy, and each binding follows its Compose's copy in the same position.
func TestComposeOwnedSetsMigrationMovesEnvironmentSets(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 14); err != nil {
		t.Fatalf("migrate to 14: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO projects (name, description, created_at, updated_at) VALUES ('p', '', 1, 1);
		INSERT INTO environments (project_id, name, created_at) VALUES (1, 'production', 1);
		INSERT INTO environments (project_id, name, created_at) VALUES (1, 'empty', 1);
		INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'a', '', 1, 1);
		INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'b', '', 1, 1);
		INSERT INTO variable_sets (environment_id, name, created_at, updated_at) VALUES (1, 'shared', 5, 6);
		INSERT INTO variable_sets (environment_id, name, created_at, updated_at) VALUES (1, 'payments', 5, 6);
		INSERT INTO variable_sets (environment_id, name, created_at, updated_at) VALUES (2, 'orphan', 5, 6);
		INSERT INTO variable_set_entries (set_id, key, value, secret) VALUES (1, 'LOG', 'debug', 0);
		INSERT INTO variable_set_entries (set_id, key, value, secret) VALUES (1, 'TOKEN', 's3cret', 1);
		INSERT INTO variable_set_entries (set_id, key, value, secret) VALUES (2, 'STRIPE', 'sk', 1);
		INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (1, 'api', 2, 0);
		INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (1, 'api', 1, 1);
		INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (2, 'worker', 1, 0);
	`); err != nil {
		t.Fatalf("seed version 14 rows: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate 14 -> latest: %v", err)
	}

	// Every set of Environment 1 is copied into both of its Composes; the set
	// in the Compose-less Environment is dropped.
	rows, err := db.Query(`
		SELECT c.name, s.name, s.created_at, s.updated_at,
			(SELECT group_concat(key || '=' || value || ':' || secret, ',')
			 FROM (SELECT * FROM variable_set_entries e WHERE e.set_id = s.id ORDER BY e.id))
		FROM variable_sets s JOIN composes c ON c.id = s.compose_id
		ORDER BY c.name, s.name`)
	if err != nil {
		t.Fatalf("read sets: %v", err)
	}
	var sets []string
	for rows.Next() {
		var compose, name, entries string
		var createdAt, updatedAt int64
		if err := rows.Scan(&compose, &name, &createdAt, &updatedAt, &entries); err != nil {
			t.Fatal(err)
		}
		sets = append(sets, fmt.Sprintf("%s/%s@%d,%d[%s]", compose, name, createdAt, updatedAt, entries))
	}
	rows.Close()
	wantSets := []string{
		"a/payments@5,6[STRIPE=sk:1]",
		"a/shared@5,6[LOG=debug:0,TOKEN=s3cret:1]",
		"b/payments@5,6[STRIPE=sk:1]",
		"b/shared@5,6[LOG=debug:0,TOKEN=s3cret:1]",
	}
	if fmt.Sprint(sets) != fmt.Sprint(wantSets) {
		t.Fatalf("sets = %v, want %v", sets, wantSets)
	}

	// Each binding points at its own Compose's copy and keeps its position.
	rows, err = db.Query(`
		SELECT c.name, b.service, s.name, b.position, s.compose_id = b.compose_id
		FROM service_bindings b
		JOIN composes c ON c.id = b.compose_id
		JOIN variable_sets s ON s.id = b.set_id
		ORDER BY c.name, b.service, b.position`)
	if err != nil {
		t.Fatalf("read bindings: %v", err)
	}
	var bindings []string
	for rows.Next() {
		var compose, service, set string
		var position int
		var sameCompose bool
		if err := rows.Scan(&compose, &service, &set, &position, &sameCompose); err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, fmt.Sprintf("%s/%s:%d=%s,%t", compose, service, position, set, sameCompose))
	}
	rows.Close()
	wantBindings := []string{"a/api:0=payments,true", "a/api:1=shared,true", "b/worker:0=shared,true"}
	if fmt.Sprint(bindings) != fmt.Sprint(wantBindings) {
		t.Fatalf("bindings = %v, want %v", bindings, wantBindings)
	}

	var violations int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("foreign key violations = %d", violations)
	}

	// The renamed tables keep their cascades: deleting a set takes its entries
	// and bindings.
	if _, err := db.Exec("DELETE FROM variable_sets WHERE compose_id = (SELECT id FROM composes WHERE name = 'a')"); err != nil {
		t.Fatal(err)
	}
	var leftover int
	if err := db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM variable_set_entries WHERE set_id NOT IN (SELECT id FROM variable_sets))
			+ (SELECT COUNT(*) FROM service_bindings WHERE compose_id = 1)`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("rows left after deleting compose a's sets = %d", leftover)
	}

	// Down returns to Environment-owned sets and Up runs again cleanly.
	if err := goose.Down(db, "."); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

func foreignKeysSetting(t *testing.T, db *sql.DB) int {
	t.Helper()
	var on int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("read PRAGMA foreign_keys: %v", err)
	}
	return on
}

func TestCanceledQueryKeepsForeignKeys(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// modernc.org/sqlite discards a connection whose query was interrupted,
	// and database/sql opens a replacement; it must come up with foreign keys on.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		var n int
		err := db.QueryRowContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 100000000) SELECT count(*) FROM c`).Scan(&n)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("round %d: error = %v, want context.DeadlineExceeded", i, err)
		}
		if got := foreignKeysSetting(t, db); got != 1 {
			t.Fatalf("round %d: foreign_keys = %d after canceled query, want 1", i, got)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'p', '', 1, 1);
		INSERT INTO environments (id, project_id, name, created_at) VALUES (1, 1, 'production', 1);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM projects WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	var environments int
	if err := db.QueryRow("SELECT COUNT(*) FROM environments").Scan(&environments); err != nil {
		t.Fatal(err)
	}
	if environments != 0 {
		t.Fatalf("environments left after deleting their project = %d, want 0", environments)
	}
}

func TestReplacementConnectionKeepsForeignKeys(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// With no idle connections allowed the pool closes this one, so the next
	// query runs on a fresh connection.
	db.SetMaxIdleConns(0)
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(1)
	if got := foreignKeysSetting(t, db); got != 1 {
		t.Fatalf("foreign_keys = %d on a replacement connection, want 1", got)
	}
}

func TestForeignKeyViolationsCountsOrphans(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO environments (id, project_id, name, created_at) VALUES (1, 99, 'production', 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	got, err := ForeignKeyViolations(context.Background(), db)
	if err != nil {
		t.Fatalf("ForeignKeyViolations() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("ForeignKeyViolations() = %d, want 1", got)
	}
}

func TestSSHKeysMigrationDiscardsSourceKeys(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 20); err != nil {
		t.Fatalf("migrate to 20: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO projects (name, description, created_at, updated_at) VALUES ('p', '', 1, 1);
		INSERT INTO environments (project_id, name, created_at) VALUES (1, 'production', 1);
		INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'a', '', 1, 1);
		INSERT INTO compose_sources (compose_id, kind, ssh_private_key, ssh_public_key, last_test_at, last_test_ok, created_at, updated_at)
			VALUES (1, 'git', 'PRIV', 'PUB', 5, 1, 1, 1);
	`); err != nil {
		t.Fatalf("seed version 20 rows: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate 20 -> latest: %v", err)
	}

	var priv, pub string
	var testAt, testOK, keyID sql.NullInt64
	if err := db.QueryRow(`SELECT ssh_private_key, ssh_public_key, last_test_at, last_test_ok, ssh_key_id
		FROM compose_sources WHERE compose_id = 1`).Scan(&priv, &pub, &testAt, &testOK, &keyID); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if priv != "" || pub != "" || testAt.Valid || testOK.Valid || keyID.Valid {
		t.Fatalf("source = %q %q %v %v %v, want discarded keys, cleared test, no key", priv, pub, testAt, testOK, keyID)
	}
	var keys int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ssh_keys`).Scan(&keys); err != nil {
		t.Fatalf("count ssh_keys: %v", err)
	}
	if keys != 0 {
		t.Fatalf("ssh_keys has %d rows, want 0", keys)
	}
}

func TestVulnerabilityScansMigrationSeedsSettings(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer db.Close()
	for _, table := range []string{"scan_settings", "scan_queue", "image_scans", "image_findings", "vulnerability_first_seen"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: count %d, err %v", table, n, err)
		}
	}
	var enabled, minute int
	var image, active string
	if err := db.QueryRow(`SELECT enabled, daily_minute, scanner_image, active_image FROM scan_settings WHERE id = 1`).Scan(&enabled, &minute, &image, &active); err != nil {
		t.Fatalf("read scan_settings: %v", err)
	}
	if enabled != 0 || minute != 180 || image != "" || active != "" {
		t.Fatalf("scan_settings = %d %d %q %q", enabled, minute, image, active)
	}
}

func TestUsersMigrationKeepsAdminAndSessions(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "panel.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 30); err != nil {
		t.Fatalf("migrate to 30: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO admin_credentials (id, email, password_hash, created_at, totp_secret, totp_last_step, failed_attempts, password_from_env)
		VALUES (1, 'admin@example.com', 'hash', 100, 'JBSWY3DPEHPK3PXP', 7, 2, 1)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sessions (token, admin_id, expires_at, created_at) VALUES ('tok', 1, 9999999999, 100)"); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate 30 -> latest: %v", err)
	}

	var (
		id                                 int64
		email, hash, secret                string
		isAdmin, lastStep, failed, fromEnv int64
	)
	if err := db.QueryRow(`
		SELECT id, email, password_hash, is_admin, totp_secret, totp_last_step, failed_attempts, password_from_env FROM users
	`).Scan(&id, &email, &hash, &isAdmin, &secret, &lastStep, &failed, &fromEnv); err != nil {
		t.Fatalf("read users: %v", err)
	}
	if id != 1 || email != "admin@example.com" || hash != "hash" || isAdmin != 1 || secret != "JBSWY3DPEHPK3PXP" || lastStep != 7 || failed != 2 || fromEnv != 1 {
		t.Fatalf("users row = %d %q %q admin=%d %q step=%d failed=%d env=%d", id, email, hash, isAdmin, secret, lastStep, failed, fromEnv)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("users count = %d, %v", count, err)
	}
	var userID int64
	if err := db.QueryRow("SELECT user_id FROM sessions WHERE token = 'tok'").Scan(&userID); err != nil || userID != 1 {
		t.Fatalf("session user_id = %d, %v", userID, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'admin_credentials'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("admin_credentials still present: %d, %v", count, err)
	}
	if violations, err := ForeignKeyViolations(context.Background(), db); err != nil || violations != 0 {
		t.Fatalf("foreign key violations = %d, %v", violations, err)
	}

	insertToken := func(hash string) error {
		_, err := db.Exec("INSERT INTO user_tokens (user_id, kind, token_hash, expires_at) VALUES (1, 'invite', ?, 200)", hash)
		return err
	}
	if err := insertToken("a"); err != nil {
		t.Fatal(err)
	}
	if err := insertToken("b"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("second open token error = %v, want a unique constraint error", err)
	}
}

func TestGrantsMigration(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO users (id, email, created_at) VALUES (2, 'member@example.com', 1);
		INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'p', '', 1, 1);
		INSERT INTO environments (id, project_id, name, created_at) VALUES (1, 1, 'production', 1);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, 1, NULL, 'viewer')"); err != nil {
		t.Fatalf("project grant: %v", err)
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, 1, 1, 'none')"); err != nil {
		t.Fatalf("environment no access: %v", err)
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, 1, NULL, 'none')"); err == nil {
		t.Fatal("a Project Grant of none was accepted")
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, 1, NULL, 'editor')"); err == nil {
		t.Fatal("a second Project Grant for the same user and project was accepted")
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, 1, 1, 'editor')"); err == nil {
		t.Fatal("a second Environment Grant for the same user and environment was accepted")
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, 1, NULL, 'owner')"); err == nil {
		t.Fatal("an unknown role was accepted")
	}
	count := func() int {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM grants").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := db.Exec("DELETE FROM environments WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("after deleting the environment, grants = %d, want 1", n)
	}
	if _, err := db.Exec("DELETE FROM users WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("after deleting the user, grants = %d, want 0", n)
	}
}

func TestAuditMigration(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var days int
	if err := db.QueryRow("SELECT retention_days FROM audit_settings WHERE id = 1").Scan(&days); err != nil || days != 90 {
		t.Fatalf("retention_days = %d, %v, want 90", days, err)
	}
	insert := `INSERT INTO audit_events (at, actor, action, outcome, project_id, target) VALUES (1, 'a@example.com', 'compose.update', ?, 999, 'x')`
	if _, err := db.Exec(insert, "maybe"); err == nil {
		t.Fatal("an unknown outcome was accepted")
	}
	if _, err := db.Exec(insert, "ok"); err != nil {
		t.Fatalf("an event naming a missing Project: %v", err)
	}
	if _, err := db.Exec("INSERT INTO audit_settings (id, retention_days) VALUES (2, 1)"); err == nil {
		t.Fatal("a second settings row was accepted")
	}
}
