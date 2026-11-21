package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"relist-panel/internal/deployments"
	"relist-panel/internal/sources"
)

const (
	migrationDir = ".migration"
	markerName   = "finalize"
)

// Summary is what the Done screen shows.
type Summary struct {
	Deployments []SummaryDeployment `json:"deployments"`
	Warnings    []string            `json:"warnings"`
	DNS         []string            `json:"dns"` // every Domain host, deduplicated
	Log         []LogEntry          `json:"log,omitempty"`
}

type SummaryDeployment struct {
	ComposeID int64  `json:"composeId"`
	Number    int    `json:"number"`
	Error     string `json:"error,omitempty"`
}

// Enqueuer queues a Deployment; *deployments.Queue has it.
type Enqueuer interface {
	Enqueue(ctx context.Context, composeID int64, r deployments.Request) (deployments.Deployment, bool, error)
}

// SourceLookup finds a Compose's Source; *sources.Store has it.
type SourceLookup interface {
	Get(ctx context.Context, composeID int64) (sources.Source, error)
}

type FinalizeDeps struct {
	Local   LocalHost
	Restart func() // nil means os.Exit(0); production re-executes the panel
	Queue   Enqueuer
	Sources SourceLookup
	Domains DomainLister
	// BeforeDeploy starts what the panel runs itself and a Compose may pull
	// from (the Registry) or connect to (a Database), before any Deployment
	// is queued. Nil when there
	// is nothing to start.
	BeforeDeploy func(context.Context) error
}

type finalizer struct{ d FinalizeDeps }

// NewFinalizer returns the Finalizer the Target runs once every item passed.
func NewFinalizer(d FinalizeDeps) Finalizer {
	if d.Restart == nil {
		d.Restart = func() { os.Exit(0) }
	}
	return &finalizer{d: d}
}

func (f *finalizer) dir() string { return filepath.Join(f.d.Local.DataDir, migrationDir) }

// Begin stops Traefik, so it cannot write its in-memory certificates over the
// copied acme.json, moves the staged data directory into place except the
// database, writes the marker and exits. The install's restart policy brings
// the panel back, and PrepareFinalize swaps the database before it is opened.
func (f *finalizer) Begin(ctx context.Context, m Manifest) error {
	reportStep(ctx, FinalizeStopTraefik)
	if err := f.d.Local.Docker.StopTraefik(ctx); err != nil {
		return fmt.Errorf("stop Traefik: %w", err)
	}
	reportStep(ctx, FinalizeMoveData)
	staged := filepath.Join(f.dir(), "staging", "data")
	entries, err := os.ReadDir(staged)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if skipInDataDir(e.Name()) {
			continue
		}
		target := filepath.Join(f.d.Local.DataDir, e.Name())
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(staged, e.Name()), target); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(struct {
		RunningComposeIDs []int64 `json:"runningComposeIds"`
	}{m.RunningComposeIDs})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.dir(), markerName), raw, 0o600); err != nil {
		return err
	}
	log.Printf("migration: data in place; restarting the panel to swap in the migrated database")
	reportStep(ctx, FinalizeRestart)
	f.d.Restart()
	return nil
}

// PrepareFinalize runs at boot before the database is opened. When the
// marker exists it replaces panel.db with the staged one, deleting any
// journal beside the old file first so none is replayed onto the new one.
// pending is true whenever the marker exists, swapped now or before.
func PrepareFinalize(dataDir string) (pending bool, err error) {
	if _, err := os.Stat(filepath.Join(dataDir, migrationDir, markerName)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	staged := filepath.Join(dataDir, migrationDir, "staging", "panel.db")
	if _, err := os.Stat(staged); errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return true, err
	}
	for _, name := range []string{"panel.db", "panel.db-journal", "panel.db-wal", "panel.db-shm"} {
		if err := os.Remove(filepath.Join(dataDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
	}
	return true, os.Rename(staged, filepath.Join(dataDir, "panel.db"))
}

// ClearCopiedState removes the old host's Migration rows from the copied
// database and cancels its unfinished Deployments, before the deployment
// worker and the lock middleware see them.
func ClearCopiedState(ctx context.Context, db *sql.DB, now time.Time) error {
	if err := ClearTables(ctx, db); err != nil {
		return err
	}
	_, err := deployments.NewStore(db).CancelUnfinished(ctx, reasonMigration, now)
	return err
}

// Complete starts Traefik again and queues a Deployment for each Compose that
// was running: skip-build for one with a Source, so nothing is rebuilt and Git
// is not contacted, and deploy for the rest.
func (f *finalizer) Complete(ctx context.Context, m Manifest) (Summary, error) {
	summary := Summary{Deployments: []SummaryDeployment{}, Warnings: []string{}, DNS: []string{}}
	reportStep(ctx, FinalizeStartTraefik)
	if err := f.d.Local.Docker.StartTraefik(ctx); err != nil {
		return summary, fmt.Errorf("start Traefik: %w", err)
	}
	if f.d.BeforeDeploy != nil {
		// A Compose pulling from this panel's own Registry would fail its
		// first pull if the Registry came up after it.
		if err := f.d.BeforeDeploy(ctx); err != nil {
			summary.Warnings = append(summary.Warnings, "could not start the Registry or a Database before deploying, so a Compose using it may fail; deploy it again once it runs: "+err.Error())
		}
	}
	reportStep(ctx, FinalizeDeploy)
	for _, id := range m.RunningComposeIDs {
		mode := deployments.ModeDeploy
		if _, err := f.d.Sources.Get(ctx, id); err == nil {
			mode = deployments.ModeSkipBuild
		} else if !errors.Is(err, sql.ErrNoRows) {
			return summary, err
		}
		entry := SummaryDeployment{ComposeID: id}
		d, _, err := f.d.Queue.Enqueue(ctx, id, deployments.Request{Mode: mode})
		if err != nil {
			entry.Error = enqueueError(err)
		} else {
			entry.Number = d.Number
		}
		summary.Deployments = append(summary.Deployments, entry)
	}
	ds, err := f.d.Domains.ListAll(ctx)
	if err != nil {
		return summary, err
	}
	for _, d := range ds {
		if !slices.Contains(summary.DNS, d.Host) {
			summary.DNS = append(summary.DNS, d.Host)
		}
	}
	slices.Sort(summary.DNS)
	return summary, nil
}

// enqueueError is a refused redeploy as the summary shows it. A zero-downtime
// refusal names each Service and reason, since the summary is all the
// operator sees of it.
func enqueueError(err error) string {
	var refused *deployments.ZeroDowntimeError
	if !errors.As(err, &refused) {
		return err.Error()
	}
	var lines []string
	for _, p := range refused.Problems {
		for _, reason := range p.Reasons {
			lines = append(lines, p.Service+" — "+reason)
		}
	}
	return "can't deploy with zero downtime: " + strings.Join(lines, "; ")
}
