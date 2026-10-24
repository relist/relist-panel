package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

const sourceSnapshotsDir = ".deployment-sources"

// executionSnapshot never participates in public Deployment serialization.
// SourceDir belongs to the Compose. It outlives its Deployment while a container
// can still bind files from it; collectSnapshots removes it once nothing does.
type executionSnapshot struct {
	Version         int
	Target          composes.DeployTarget
	Files           *composes.ExecutionSnapshot
	Source          *sources.Source
	SourceDir       string
	IdentityVersion int
	Legacy          bool
	release         func()
}

func decodeSnapshot(raw string, snapshot *executionSnapshot) error {
	if err := json.Unmarshal([]byte(raw), snapshot); err != nil {
		return err
	}
	if snapshot.Version != 1 {
		return errors.New("unsupported deployment snapshot version")
	}
	snapshot.Target.Snapshot = snapshot.Files
	snapshot.Target.Compose.IdentityVersion = snapshot.IdentityVersion
	return nil
}

func (s *Store) snapshot(ctx context.Context, id int64) (*executionSnapshot, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT snapshot FROM deployment_snapshots WHERE deployment_id = ?`, id).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var snapshot executionSnapshot
	if err := decodeSnapshot(raw, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

type alreadyQueued struct{ deployment Deployment }

func (*alreadyQueued) Error() string { return "deployment is already queued" }

func (q *Queue) capture(ctx context.Context, composeID int64, mode Mode, deduplicate bool) (*executionSnapshot, error) {
	t, err := q.d.Composes.Target(ctx, composeID)
	if err != nil {
		return nil, err
	}
	release, err := q.d.Gate.Acquire(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	captured := false
	defer func() {
		if !captured {
			release()
		}
	}()
	if deduplicate {
		if existing, err := queuedFor(ctx, q.d.Store.db, composeID); err == nil {
			return nil, &alreadyQueued{deployment: existing}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	// Re-read under the gate: an earlier parent delete/edit may have won it.
	t, err = q.d.Composes.Target(ctx, composeID)
	if err != nil {
		return nil, err
	}
	t, err = q.d.Composes.Capture(ctx, t)
	if err != nil {
		return nil, err
	}
	snapshot := &executionSnapshot{Version: 1, Target: t, Files: t.Snapshot, IdentityVersion: t.Compose.IdentityVersion, release: release}
	src, err := q.d.Sources.Get(ctx, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		captured = true
		return snapshot, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot.Source = &src
	dir := composes.ComposeDir(t.Request)
	if src.Kind == sources.KindGit && mode == ModeBuild {
		revision, err := q.d.Fetcher.ResolveRevision(ctx, src, dir)
		if err != nil {
			return nil, fmt.Errorf("could not capture Source revision: %w", err)
		}
		snapshot.Source.PinnedSHA = revision
	}
	root := filepath.Join(dir, sourceSnapshotsDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	private, err := os.MkdirTemp(root, "source-")
	if err != nil {
		return nil, err
	}
	snapshot.SourceDir = private
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(private)
			_ = os.Remove(root)
		}
	}()
	copyCtx, copyCancel := context.WithTimeout(ctx, time.Minute)
	defer copyCancel()
	budget := &sourceCopyBudget{}
	if mode == ModeBuild && t.NeedsComposeBuildContext() {
		if err := copySourceWithBudget(copyCtx, dir, private, budget, "src", sourceSnapshotsDir, logDirName, ".ssh", "sets", ".build-sets", ".env", "docker-compose.yml", ".build.env", ".build-compose.yml"); err != nil {
			return nil, fmt.Errorf("could not snapshot build context: %w", err)
		}
	}
	// Skip-build reuses images, but runtime bind/env/config files still need an
	// immutable copy of the already available Source, rather than a new fetch.
	if src.Kind == sources.KindZip || mode != ModeBuild {
		origin := filepath.Join(dir, "src")
		if src.Kind == sources.KindGit && src.TreeDirectory != "" {
			origin = src.TreeDirectory
		}
		if err := copySourceWithBudget(copyCtx, origin, filepath.Join(private, "src"), budget); err != nil && !unfetchedCanonicalSource(src, origin, err) {
			return nil, fmt.Errorf("could not snapshot Source files: %w", err)
		}
	}
	t, err = t.WithSourceDirectory(filepath.Join(private, "src"))
	if err != nil {
		return nil, err
	}
	snapshot.Target, snapshot.Files = t, t.Snapshot
	ok = true
	captured = true
	return snapshot, nil
}

func unfetchedCanonicalSource(src sources.Source, origin string, err error) bool {
	var pathErr *fs.PathError
	return src.Kind == sources.KindGit && src.TreeDirectory == "" && errors.Is(err, os.ErrNotExist) && errors.As(err, &pathErr) && pathErr.Op == "lstat" && filepath.Clean(pathErr.Path) == filepath.Clean(origin)
}

// copySource copies bytes, never hardlinks. Safe relative links stay relative;
// links outside the tree and special files cannot escape the Source boundary.
func copySource(ctx context.Context, source, target string, exclude ...string) error {
	return copySourceWithBudget(ctx, source, target, &sourceCopyBudget{}, exclude...)
}

type sourceCopyBudget struct {
	size  int64
	files int
}

func copySourceWithBudget(ctx context.Context, source, target string, budget *sourceCopyBudget, exclude ...string) error {
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	var directories []directoryMode
	// Opened at the first link, so a missing source still fails with
	// WalkDir's lstat error.
	var sourceRoot *os.Root
	defer func() {
		if sourceRoot != nil {
			sourceRoot.Close()
		}
	}()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		root := strings.Split(relative, string(os.PathSeparator))[0]
		for _, name := range exclude {
			if root == name {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			_, existingErr := os.Stat(destination)
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return err
			}
			if errors.Is(existingErr, os.ErrNotExist) {
				directories = append(directories, directoryMode{path: destination, mode: info.Mode().Perm() & 0o777})
			}
			return nil
		}
		budget.files++
		if budget.files > 100000 {
			return errors.New("Source snapshot exceeds 100000 files or 4 GiB")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if sourceRoot == nil {
				if sourceRoot, err = os.OpenRoot(source); err != nil {
					return err
				}
			}
			// Resolve the way the kernel will, so links routed through other
			// links are caught too. One that dangles inside the tree is fine.
			if _, err := sourceRoot.Stat(relative); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return errors.New("Source contains a symlink outside its directory")
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Source contains an unsupported file type")
		}
		remaining := int64(4<<30) - budget.size
		if info.Size() > remaining {
			return errors.New("Source snapshot exceeds 100000 files or 4 GiB")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0o777)
		if err != nil {
			_ = in.Close()
			return err
		}
		if err := out.Chmod(info.Mode().Perm() & 0o777); err != nil {
			_ = in.Close()
			_ = out.Close()
			return err
		}
		copied, copyErr := io.Copy(out, io.LimitReader(&contextReader{ctx: ctx, reader: in}, remaining+1))
		budget.size += copied
		if copied > remaining {
			copyErr = errors.New("Source snapshot exceeds 100000 files or 4 GiB")
		}
		_ = in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return err
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func discardCaptured(snapshot *executionSnapshot) {
	if snapshot != nil && snapshot.SourceDir != "" {
		_ = os.RemoveAll(snapshot.SourceDir)
		_ = os.Remove(filepath.Dir(snapshot.SourceDir))
	}
}

func (s *Store) saveLegacySnapshot(ctx context.Context, id int64, snapshot *executionSnapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO deployment_snapshots (deployment_id, snapshot) VALUES (?, ?) ON CONFLICT(deployment_id) DO NOTHING`, id, string(raw))
	return err
}
