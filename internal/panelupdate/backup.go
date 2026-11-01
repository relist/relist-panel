package panelupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Backup names the copies in a run directory, so the Updater can put every
// Install file back where it was. Paths are the Install's own.
type Backup struct {
	Config []BackupFile `json:"config"`
	// Env is nil when the Install had no .env.
	Env *BackupFile `json:"env"`
}

// BackupFile is one backed-up file: where it lives on the host, and its copy
// in the run directory.
type BackupFile struct {
	Path string `json:"path"`
	Copy string `json:"copy"`
}

const (
	backupDB   = "panel.db"
	backupMeta = "backup.json"
)

// ReadBackup reads a run directory's backup.json.
func ReadBackup(dir string) (Backup, error) {
	var b Backup
	data, err := os.ReadFile(filepath.Join(dir, backupMeta))
	if err != nil {
		return b, err
	}
	return b, json.Unmarshal(data, &b)
}

// pruneRuns removes every run directory but keep, which has just been backed
// up completely, and but the newest run that ended rollback-failed: its backup
// is what the manual restore commands point at. That one goes only once a
// later run has succeeded.
func pruneRuns(dataDir, keep string) {
	ids, err := runIDs(dataDir)
	if err != nil {
		log.Printf("panelupdate: list old runs: %v", err)
		return
	}
	state := func(id string) string {
		st, err := ReadStatus(RunDir(dataDir, id))
		if err != nil {
			return ""
		}
		return st.State
	}
	// Walk from the newest: a succeeded run settles every older failure.
	retain, settled := "", false
	for i := len(ids) - 1; i >= 0; i-- {
		switch state(ids[i]) {
		case StateSucceeded:
			settled = true
		case StateRollbackFailed:
			if !settled && ids[i] != keep && retain == "" {
				retain = ids[i]
			}
		}
	}
	for _, id := range ids {
		if id == keep || id == retain {
			continue
		}
		if err := os.RemoveAll(RunDir(dataDir, id)); err != nil {
			log.Printf("panelupdate: remove run %s: %v", id, err)
		}
	}
}

// writeBackup is spec Apply step 5: panel.db (VACUUM INTO, from the Updater's
// own connection), every Install config file and .env, into the run directory.
func writeBackup(ctx context.Context, dataDir, runDir string, in Install, files InstallFiles) error {
	if err := vacuumInto(ctx, filepath.Join(dataDir, "panel.db"), filepath.Join(runDir, backupDB)); err != nil {
		return err
	}
	var meta Backup
	for i, path := range in.ConfigFiles {
		name := fmt.Sprintf("config-%d%s", i, filepath.Ext(path))
		if err := os.WriteFile(filepath.Join(runDir, name), files.Config[i], 0o600); err != nil {
			return err
		}
		meta.Config = append(meta.Config, BackupFile{Path: path, Copy: name})
	}
	if len(files.Env) > 0 {
		if err := os.WriteFile(filepath.Join(runDir, "env"), files.Env, 0o600); err != nil {
			return err
		}
		meta.Env = &BackupFile{Path: in.EnvFile(), Copy: "env"}
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, backupMeta), data, 0o600)
}

// vacuumInto copies the database at dbPath to dst with VACUUM INTO, through a
// connection of its own. It never runs schema migrations, so an Updater from a
// newer image cannot change the database it is about to back up.
func vacuumInto(ctx context.Context, dbPath, dst string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	return nil
}
