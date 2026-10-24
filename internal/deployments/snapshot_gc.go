package deployments

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"relist-panel/internal/composes"
)

// MountLister reports the host source of every mount of a Compose project's
// containers, stopped ones included.
type MountLister interface {
	MountedSources(ctx context.Context, projectName string) ([]string, error)
}

// snapshotLookupTimeout bounds each wait of a collection that holds the Project
// gate: for the gate itself, and for Docker and the database to say what is
// referenced. A var so tests can shorten it.
var snapshotLookupTimeout = 30 * time.Second

// snapshotDirPrefix is what capture's os.MkdirTemp names its directories.
const snapshotDirPrefix = "source-"

// collectSnapshots removes the Compose's Deployment source snapshots that
// nothing references any more (ADR-0017). It deletes only when it is sure: any
// failure to learn what is referenced leaves every directory in place for this
// round.
//
// Kept are the snapshots of Deployments that have not settled, the tree the
// Source's materialization points at, and every directory holding a path some
// container of the Compose mounts, running or not.
func (q *Queue) collectSnapshots(ctx context.Context, composeID int64) {
	if q.d.Mounts == nil {
		return
	}
	target, err := q.d.Composes.Target(ctx, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		logf("collect snapshots of compose %d: %v", composeID, err)
		return
	}
	root := filepath.Join(composes.ComposeDir(target.Request), sourceSnapshotsDir)
	if _, err := os.Lstat(root); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logf("collect snapshots of compose %d: %v", composeID, err)
		}
		return
	}

	// The gate keeps a Deploy or a capture from adding a reference while the
	// references are being listed. The volumes hold makes open Volume reads,
	// which a stopped Compose's ./src can still resolve to a snapshot, end
	// first; gate before volumes, as everywhere.
	gateCtx, cancel := context.WithTimeout(ctx, snapshotLookupTimeout)
	release, err := q.d.Gate.Acquire(gateCtx, target.ProjectID)
	cancel()
	if err != nil {
		logf("collect snapshots of compose %d: project gate: %v", composeID, err)
		return
	}
	defer release()
	volumesCtx, cancelVolumes := context.WithTimeout(ctx, snapshotLookupTimeout)
	releaseVolumes, err := q.d.Gate.AcquireVolumes(volumesCtx, target.ProjectID)
	cancelVolumes()
	if err != nil {
		logf("collect snapshots of compose %d: project volumes: %v", composeID, err)
		return
	}
	defer releaseVolumes()

	// A hung daemon must not hold the Project gate or the worker for ever.
	lookupCtx, cancelLookup := context.WithTimeout(ctx, snapshotLookupTimeout)
	defer cancelLookup()
	mounted, err := q.d.Mounts.MountedSources(lookupCtx, target.Request.ProjectName)
	if err != nil {
		logf("collect snapshots of compose %d: list mounts: %v", composeID, err)
		return
	}
	keep, err := q.referencedSnapshotPaths(lookupCtx, composeID)
	if err != nil {
		logf("collect snapshots of compose %d: %v", composeID, err)
		return
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		logf("collect snapshots of compose %d: %v", composeID, err)
		return
	}
	for _, entry := range entries {
		// Only real snapshot directories: never a symlink, whose target
		// lies outside, nor anything this code did not create.
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), snapshotDirPrefix) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if snapshotReferenced(dir, keep, mounted) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			logf("remove snapshot %s: %v", dir, err)
		}
	}
	// Leave nothing behind once the last snapshot is gone; fails harmlessly
	// while the directory still holds something.
	_ = os.Remove(root)
}

// referencedSnapshotPaths lists the paths inside snapshots that queued or
// running Deployments of the Compose will use, and the one its Source's
// materialization points at. An unsettled status this code does not know is
// treated as unsettled.
func (q *Queue) referencedSnapshotPaths(ctx context.Context, composeID int64) ([]string, error) {
	var paths []string
	var dir string
	err := q.d.Store.db.QueryRowContext(ctx, `SELECT source_dir FROM compose_source_materializations WHERE compose_id = ?`, composeID).Scan(&dir)
	if err == nil {
		paths = append(paths, dir)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := q.d.Store.db.QueryContext(ctx, `SELECT s.snapshot FROM deployment_snapshots s JOIN deployments d ON d.id = s.deployment_id
		WHERE d.compose_id = ? AND d.status NOT IN (?, ?, ?)`, composeID, StatusSucceeded, StatusFailed, StatusCanceled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var snapshot executionSnapshot
		if err := decodeSnapshot(raw, &snapshot); err != nil {
			return nil, err
		}
		if snapshot.SourceDir != "" {
			paths = append(paths, snapshot.SourceDir)
		}
	}
	return paths, rows.Err()
}

// snapshotReferenced reports whether dir is, or holds, one of the keep paths
// or one of the mounted sources, or lies inside a mounted source.
func snapshotReferenced(dir string, keep, mounted []string) bool {
	for _, d := range pathForms(dir) {
		for _, k := range keep {
			for _, kf := range pathForms(k) {
				if within(d, kf) {
					return true
				}
			}
		}
		for _, m := range mounted {
			for _, mf := range pathForms(m) {
				if within(d, mf) || within(mf, d) {
					return true
				}
			}
		}
	}
	return false
}

// pathForms is p cleaned, plus its symlink-resolved form when it has one, so a
// mount recorded through a link still matches.
func pathForms(p string) []string {
	forms := []string{filepath.Clean(p)}
	if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != forms[0] {
		forms = append(forms, resolved)
	}
	return forms
}

// within reports whether child is parent or lies beneath it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
