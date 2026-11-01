package panelupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// States of an update run, as written to status.json. The panel writes the
// first three, the Updater the rest, and the watcher writes interrupted.
const (
	StateQuiescing       = "quiescing"
	StateBackingUp       = "backing-up"
	StateStartingUpdater = "starting-updater"
	StateWritingFiles    = "writing-files"
	StateComposeUp       = "compose-up"
	StateWaitingHealth   = "waiting-health"
	StateSucceeded       = "succeeded"
	StateRollingBack     = "rolling-back"
	StateRolledBack      = "rolled-back"
	StateRollbackFailed  = "rollback-failed"
	StateInterrupted     = "interrupted"
)

const (
	statusFile  = "status.json"
	runsDirName = ".panel-update"
	runIDLayout = "20060102T150405Z"
	updaterBase = "relist-panel-updater-"
)

// IsTerminal reports whether a run in state is over. Any other state keeps the
// update lock on.
func IsTerminal(state string) bool {
	switch state {
	case StateSucceeded, StateRolledBack, StateRollbackFailed, StateInterrupted:
		return true
	}
	return false
}

// RunStatus is status.json, the one file the panel and the Updater share. A
// writer replaces it whole and atomically, so a reader never sees half of it.
type RunStatus struct {
	Run         string `json:"run"`
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	StartedAt   int64  `json:"startedAt"`
	// UpdatedAt is when the file was last written.
	UpdatedAt int64 `json:"updatedAt"`
}

var runIDPattern = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// NewRunID is a UTC timestamp, so the highest id is the newest run.
func NewRunID(now time.Time) string { return now.UTC().Format(runIDLayout) }

// UpdaterName is the Updater Container's name for a run.
func UpdaterName(run string) string { return updaterBase + run }

// RunsDir holds every run directory.
func RunsDir(dataDir string) string { return filepath.Join(dataDir, runsDirName) }

// RunDir is one run's directory: status.json, the backup and plan.json.
func RunDir(dataDir, run string) string { return filepath.Join(RunsDir(dataDir), run) }

// WriteStatus replaces dir/status.json atomically: a synced temp file in the
// same directory, then a rename.
func WriteStatus(dir string, st RunStatus, now time.Time) error {
	st.UpdatedAt = now.Unix()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, statusFile))
}

// ReadStatus reads dir/status.json. A missing file is fs.ErrNotExist.
func ReadStatus(dir string) (RunStatus, error) {
	data, err := os.ReadFile(filepath.Join(dir, statusFile))
	if err != nil {
		return RunStatus{}, err
	}
	var st RunStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return RunStatus{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, statusFile), err)
	}
	return st, nil
}

// runIDs lists the run directories, oldest first.
func runIDs(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(RunsDir(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && runIDPattern.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// NewestRun is the status of the highest run id; ok is false when there is no
// run. A run directory whose status.json is missing or unparsable reads as
// interrupted: the file is replaced atomically, so no Updater ever went on
// from such a directory.
func NewestRun(dataDir string) (st RunStatus, ok bool, err error) {
	ids, err := runIDs(dataDir)
	if err != nil || len(ids) == 0 {
		return RunStatus{}, false, err
	}
	id := ids[len(ids)-1]
	st, err = ReadStatus(RunDir(dataDir, id))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return RunStatus{Run: id, State: StateInterrupted, Error: "the run left no status.json"}, true, nil
	case err != nil:
		return RunStatus{Run: id, State: StateInterrupted, Error: err.Error()}, true, nil
	}
	st.Run = id
	return st, true, nil
}
