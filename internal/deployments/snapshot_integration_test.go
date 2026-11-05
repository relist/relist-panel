package deployments

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
	"relist-panel/internal/sshkeys"
)

type snapshotRunner struct {
	contents, mountedPaths []string
	builds                 int
	dockerfiles            []string
}

func (r *snapshotRunner) Up(_ context.Context, req composes.RunRequest, _ composes.UpOptions) error {
	content, err := os.ReadFile(req.ComposeFilePath)
	if err != nil {
		return err
	}
	var doc struct {
		Services map[string]struct{ Volumes []string }
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return err
	}
	path, _, _ := strings.Cut(doc.Services["web"].Volumes[0], ":")
	app, err := os.ReadFile(filepath.Join(path, "app.txt"))
	if err != nil {
		return err
	}
	r.contents = append(r.contents, string(app))
	r.mountedPaths = append(r.mountedPaths, path)
	return nil
}
func (r *snapshotRunner) Build(_ context.Context, req composes.RunRequest, _ composes.BuildOptions) error {
	content, err := os.ReadFile(req.ComposeFilePath)
	if err != nil {
		return err
	}
	var doc struct {
		Services map[string]struct {
			Build struct{ Context, Dockerfile string }
		}
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return err
	}
	build := doc.Services["web"].Build
	path := build.Dockerfile
	if !filepath.IsAbs(path) {
		path = filepath.Join(build.Context, path)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r.dockerfiles = append(r.dockerfiles, string(file))
	r.builds++
	return nil
}

func TestSourceBuildSnapshotsComposeRelativeDockerfileOutsideSrc(t *testing.T) {
	for _, absolute := range []bool{true, false} {
		t.Run(fmt.Sprintf("absolute=%v", absolute), func(t *testing.T) {
			db, id, _ := newDB(t)
			runner := &snapshotRunner{}
			if _, err := db.Exec(`UPDATE composes SET identity_version=1 WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			svc := composes.New(db, runner, t.TempDir())
			target, err := svc.Target(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			dir := composes.ComposeDir(target.Request)
			if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "src", "app.txt"), []byte("code"), 0o644); err != nil {
				t.Fatal(err)
			}
			dockerfile := filepath.Join(dir, "Dockerfile")
			if err := os.WriteFile(dockerfile, []byte("FROM alpine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := dockerfile
			if !absolute {
				path = "../Dockerfile"
			}
			content := fmt.Sprintf("services:\n  web:\n    build: {context: ./src, dockerfile: %q}\n    volumes: ['./src:/app']\n", path)
			if _, err := db.Exec(`UPDATE composes SET yaml_content=?, identity_version=1 WHERE id=?`, content, id); err != nil {
				t.Fatal(err)
			}
			src := sources.NewStore(db)
			if err := src.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindZip, ZipName: "source.zip"}); err != nil {
				t.Fatal(err)
			}
			q := NewQueue(Deps{Store: NewStore(db), Sources: src, Composes: svc, Fetcher: &countedGit{}, Builder: runner, Gate: &fakeGate{rec: &recorder{}}})
			queued, _, err := q.Enqueue(context.Background(), id, Request{Mode: ModeBuild})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dockerfile, []byte("FROM busybox\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := q.process(context.Background(), queued); err != nil {
				t.Fatal(err)
			}
			if len(runner.dockerfiles) != 1 || runner.dockerfiles[0] != "FROM alpine\n" {
				t.Fatalf("Dockerfile escaped frozen build inputs:%v", runner.dockerfiles)
			}
		})
	}
}
func (*snapshotRunner) Down(context.Context, composes.RunRequest) error            { return nil }
func (*snapshotRunner) DownWithVolumes(context.Context, composes.RunRequest) error { return nil }
func (*snapshotRunner) Restart(context.Context, composes.RunRequest) error         { return nil }
func (*snapshotRunner) Status(context.Context, composes.RunRequest) (composes.ComposeStatus, error) {
	return composes.ComposeStatus{}, nil
}
func (*snapshotRunner) Config(context.Context, composes.RunRequest) ([]byte, error) {
	return []byte(`{"services":{}}`), nil
}
func (*snapshotRunner) Scale(context.Context, composes.RunRequest, composes.ScaleOptions) error {
	return nil
}
func (*snapshotRunner) Logs(context.Context, composes.RunRequest) (string, error) { return "", nil }

type countedGit struct {
	sources.Git
	lookups, fetches int
}

func (g *countedGit) ResolveRevision(ctx context.Context, src sources.Source, dir string) (string, error) {
	g.lookups++
	return g.Git.ResolveRevision(ctx, src, dir)
}
func (g *countedGit) Fetch(ctx context.Context, src sources.Source, dir string, out io.Writer) (sources.Ref, error) {
	g.fetches++
	return g.Git.Fetch(ctx, src, dir, out)
}

func localGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Snapshot test", "-c", "user.email=snapshot@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	content, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git%v:%v %s", args, err, content)
	}
	return strings.TrimSpace(string(content))
}

func TestGitSnapshotBuildThenOfflineSkipKeepsPinnedRuntimeFiles(t *testing.T) {
	for _, build := range []bool{true, false} {
		t.Run(fmt.Sprintf("build=%v", build), func(t *testing.T) {
			db, id, _ := newDB(t)
			content := "services:\n  web:\n    image: sample\n    volumes: ['./src:/app']\n"
			if build {
				content += "    build: {context: '.', dockerfile: './src/Dockerfile'}\n"
			}
			if _, err := db.Exec(`UPDATE composes SET yaml_content=?, identity_version=1 WHERE id=?`, content, id); err != nil {
				t.Fatal(err)
			}
			work := t.TempDir()
			localGit(t, work, "init")
			if err := os.WriteFile(filepath.Join(work, "app.txt"), []byte("first"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			localGit(t, work, "add", ".")
			localGit(t, work, "commit", "-m", "first")
			sha := localGit(t, work, "rev-parse", "HEAD")
			sourceStore := sources.NewStore(db)
			key, err := sshkeys.NewStore(db).Create(context.Background(), "integration")
			if err != nil {
				t.Fatal(err)
			}
			if err := sourceStore.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindGit, RepoURL: "file://" + work, Branch: "main", SSHKeyID: &key.ID}); err != nil {
				t.Fatal(err)
			}
			runner := &snapshotRunner{}
			git := &countedGit{}
			svc := composes.New(db, runner, t.TempDir())
			store := NewStore(db)
			q := NewQueue(Deps{Store: store, Sources: sourceStore, Composes: svc, Fetcher: git, Builder: runner, Gate: &fakeGate{rec: &recorder{}}, Keep: 1})
			first, _, err := q.Enqueue(context.Background(), id, Request{Mode: ModeBuild})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "app.txt"), []byte("second"), 0o644); err != nil {
				t.Fatal(err)
			}
			localGit(t, work, "add", ".")
			localGit(t, work, "commit", "-m", "second")
			if err := q.process(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			completed, err := store.Get(context.Background(), id, first.Number)
			if err != nil || completed.Status != StatusSucceeded || completed.SourceRef != sha {
				t.Fatalf("first deployment=%+v err=%v", completed, err)
			}
			if len(runner.contents) != 1 || runner.contents[0] != "first" {
				t.Fatalf("moving branch runtime=%v", runner.contents)
			}
			if err := os.Rename(work, work+"-offline"); err != nil {
				t.Fatal(err)
			}
			defer os.Rename(work+"-offline", work)
			second, _, err := q.Enqueue(context.Background(), id, Request{Mode: ModeSkipBuild})
			if err != nil {
				t.Fatalf("skip-build unnecessarily contacted offline repo: %v", err)
			}
			if err := q.process(context.Background(), second); err != nil {
				t.Fatal(err)
			}
			if len(runner.contents) != 2 || runner.contents[1] != "first" || git.lookups != 1 || git.fetches != 1 {
				t.Fatalf("skip runtime=%v lookups=%d fetches=%d", runner.contents, git.lookups, git.fetches)
			}
			if _, err := os.Stat(runner.mountedPaths[0]); err != nil {
				t.Fatalf("pruning history deleted an earlier mounted source: %v", err)
			}
		})
	}
}

func TestFailedSourceCaptureLeavesNoDeploymentOrPrivateTree(t *testing.T) {
	h := newHarness(t)
	h.zipSource(h.a)
	if err := os.Symlink("/etc/passwd", filepath.Join(h.comp.dirs[h.a], "src", "unsafe")); err != nil {
		t.Fatal(err)
	}
	h.newQueue()
	if _, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeBuild}); err == nil {
		t.Fatal("unsafe Source accepted")
	}
	var count int
	if err := h.store.db.QueryRow(`SELECT count(*) FROM deployments`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("capture failure queued%d jobs", count)
	}
	if _, err := os.Stat(filepath.Join(h.comp.dirs[h.a], sourceSnapshotsDir)); !os.IsNotExist(err) {
		t.Fatalf("capture failure left private files:%v", err)
	}
}

func TestCopyPreservesSourcePermissionsAndInternalLinks(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	if err := os.Chmod(from, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "file"), []byte("code"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(from, "file"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(from, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(from, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, ".git", "HEAD"), []byte("immutable metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copySource(context.Background(), from, to); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(to, "file"))
	root, _ := os.Stat(to)
	content, err := os.ReadFile(filepath.Join(to, "link"))
	gitMetadata, gitErr := os.ReadFile(filepath.Join(to, ".git", "HEAD"))
	if gitErr != nil || string(gitMetadata) != "immutable metadata" {
		t.Fatalf("uploaded metadata lost:%q %v", gitMetadata, gitErr)
	}
	if info.Mode().Perm() != 0o664 || root.Mode().Perm() != 0o755 || err != nil || string(content) != "code" {
		t.Fatalf("copy changed supported Source: file=%v root=%v content=%q err=%v", info.Mode(), root.Mode(), content, err)
	}
}

func TestCopyRefusesSymlinksThatResolveOutside(t *testing.T) {
	cases := map[string]map[string]string{
		"absolute": {"host": "/etc/passwd"},
		"climbs":   {"up": "../outside"},
		// Each link passes a lexical check; only the kernel's walk escapes.
		"chained": {"d/e/sl": "../..", "d/e/s": "sl/../../.."},
	}
	for name, links := range cases {
		t.Run(name, func(t *testing.T) {
			from, to := t.TempDir(), filepath.Join(t.TempDir(), "copy")
			for link, target := range links {
				p := filepath.Join(from, link)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			if err := copySource(context.Background(), from, to); err == nil {
				t.Fatal("escaping symlink copied")
			}
		})
	}
}

// PolicyConfig answers what Config does: the fake's config is the policy's.
func (r *snapshotRunner) Pull(ctx context.Context, req composes.RunRequest, opts composes.PullOptions) error {
	return nil
}

func (r *snapshotRunner) ImageLabels(ctx context.Context, image string) (map[string]string, error) {
	return nil, nil
}

func (r *snapshotRunner) PolicyConfig(ctx context.Context, req composes.RunRequest) ([]byte, error) {
	return r.Config(ctx, req)
}
