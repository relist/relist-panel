package sources

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "protocol.file.allow=always", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// remoteRepo is a bare repository plus a worktree that pushes to it.
type remoteRepo struct {
	t    *testing.T
	url  string
	work string
}

func newRemoteRepo(t *testing.T) *remoteRepo {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	for _, d := range []string{bare, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, bare, "init", "--bare")
	runGit(t, work, "init")
	runGit(t, work, "remote", "add", "origin", "file://"+bare)
	return &remoteRepo{t: t, url: "file://" + bare, work: work}
}

// commit writes a file, commits it and force-pushes branch; it returns the SHA.
func (r *remoteRepo) commit(branch, file, content, msg string) string {
	r.t.Helper()
	runGit(r.t, r.work, "checkout", "-B", branch)
	p := filepath.Join(r.work, file)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	runGit(r.t, r.work, "add", "-A")
	runGit(r.t, r.work, "commit", "-m", msg)
	runGit(r.t, r.work, "push", "--force", "origin", branch)
	return runGit(r.t, r.work, "rev-parse", "HEAD")
}

func gitSource(url, branch string) Source {
	return Source{Kind: KindGit, RepoURL: url, Branch: branch, PrivateKey: "not-a-real-key\n"}
}

func TestFetchFirstClone(t *testing.T) {
	r := newRemoteRepo(t)
	sha := r.commit("main", "app.txt", "hello", "first commit")
	dir := t.TempDir()
	var out bytes.Buffer

	ref, err := Git{}.Fetch(context.Background(), gitSource(r.url, "main"), dir, &out)
	if err != nil {
		t.Fatalf("Fetch: %v\n%s", err, out.String())
	}
	if ref.SHA != sha || ref.Subject != "first commit" {
		t.Fatalf("ref = %+v, want %s / first commit", ref, sha)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src", "app.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("app.txt = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", MarkerName)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("no output was written to out")
	}
}

func TestFetchAfterForcePush(t *testing.T) {
	r := newRemoteRepo(t)
	r.commit("main", "app.txt", "one", "one")
	dir := t.TempDir()
	src := gitSource(r.url, "main")
	if _, err := (Git{}).Fetch(context.Background(), src, dir, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "src", "stray.txt")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, r.work, "reset", "--hard", "HEAD")
	runGit(t, r.work, "commit", "--amend", "-m", "rewritten", "--allow-empty")
	runGit(t, r.work, "push", "--force", "origin", "main")
	want := runGit(t, r.work, "rev-parse", "HEAD")

	ref, err := Git{}.Fetch(context.Background(), src, dir, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.SHA != want || ref.Subject != "rewritten" {
		t.Fatalf("ref = %+v, want %s", ref, want)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("stray file survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", MarkerName)); err != nil {
		t.Fatalf("marker removed: %v", err)
	}
}

func TestFetchBranchWithSlash(t *testing.T) {
	r := newRemoteRepo(t)
	r.commit("main", "a.txt", "main", "on main")
	sha := r.commit("feature/checkout-v2", "b.txt", "feature", "on feature")
	dir := t.TempDir()

	ref, err := Git{}.Fetch(context.Background(), gitSource(r.url, "feature/checkout-v2"), dir, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.SHA != sha {
		t.Fatalf("sha = %s, want %s", ref.SHA, sha)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "b.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestFetchSubmodules(t *testing.T) {
	sub := newRemoteRepo(t)
	sub.commit("main", "lib.txt", "lib", "lib")
	r := newRemoteRepo(t)
	r.commit("main", "a.txt", "a", "a")
	runGit(t, r.work, "submodule", "add", sub.url, "vendorlib")
	runGit(t, r.work, "commit", "-m", "add submodule")
	runGit(t, r.work, "push", "origin", "main")

	for _, on := range []bool{true, false} {
		dir := t.TempDir()
		s := gitSource(r.url, "main")
		s.Submodules = on
		if _, err := (Git{}).Fetch(context.Background(), s, dir, &bytes.Buffer{}); err != nil {
			t.Fatalf("submodules=%v: %v", on, err)
		}
		_, err := os.Stat(filepath.Join(dir, "src", "vendorlib", "lib.txt"))
		if on && err != nil {
			t.Fatalf("submodule file missing: %v", err)
		}
		if !on && err == nil {
			t.Fatal("submodule file present with submodules off")
		}
	}
}

func TestFetchRefusesForeignSrc(t *testing.T) {
	r := newRemoteRepo(t)
	r.commit("main", "a.txt", "a", "a")
	dir := t.TempDir()
	mine := filepath.Join(dir, "src", "mine.txt")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Git{}.Fetch(context.Background(), gitSource(r.url, "main"), dir, &bytes.Buffer{})
	if !errors.Is(err, ErrForeignSrc) {
		t.Fatalf("err = %v, want ErrForeignSrc", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "src"))
	if len(entries) != 1 || entries[0].Name() != "mine.txt" {
		t.Fatalf("src was touched: %v", entries)
	}
}

func TestListBranches(t *testing.T) {
	r := newRemoteRepo(t)
	m := r.commit("main", "a.txt", "a", "a")
	f := r.commit("feature/checkout-v2", "b.txt", "b", "b")
	dir := filepath.Join(t.TempDir(), "not-yet-created")

	got, err := Git{}.ListBranches(context.Background(), gitSource(r.url, ""), dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	want := []Branch{{"feature/checkout-v2", f}, {"main", m}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("branches = %+v, want %+v", got, want)
	}
}

func TestSSHCommand(t *testing.T) {
	got := sshCommand("/tmp/relist-key-1", "/c/.ssh/known_hosts")
	for _, want := range []string{"-i /tmp/relist-key-1", "IdentitiesOnly=yes", "StrictHostKeyChecking=accept-new", "UserKnownHostsFile=/c/.ssh/known_hosts"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing %q", got, want)
		}
	}
}

func TestKeyFileRemoved(t *testing.T) {
	r := newRemoteRepo(t)
	r.commit("main", "a.txt", "a", "a")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if _, err := (Git{}).Fetch(context.Background(), gitSource(r.url, "main"), t.TempDir(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	left, _ := filepath.Glob(filepath.Join(os.TempDir(), "relist-key-*"))
	if os.TempDir() != tmp {
		t.Fatalf("TempDir = %s, want %s", os.TempDir(), tmp)
	}
	if len(left) != 0 {
		t.Fatalf("key files left: %v", left)
	}
}

func TestFetchMarkerSymlinkNotFollowed(t *testing.T) {
	r := newRemoteRepo(t)
	if err := os.Symlink("../outside.txt", filepath.Join(r.work, MarkerName)); err != nil {
		t.Fatal(err)
	}
	r.commit("main", "a.txt", "a", "with marker link")
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.txt")

	for i := 0; i < 2; i++ {
		if _, err := (Git{}).Fetch(context.Background(), gitSource(r.url, "main"), dir, &bytes.Buffer{}); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
		if _, err := os.Lstat(outside); !os.IsNotExist(err) {
			t.Fatalf("fetch %d: outside file exists: %v", i, err)
		}
		info, err := os.Lstat(filepath.Join(dir, "src", MarkerName))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("fetch %d: marker not a regular file: %v %v", i, info, err)
		}
	}
}

func TestFetchRefusesEscapingSymlinks(t *testing.T) {
	cases := map[string]struct {
		links    map[string]string
		escaping string
	}{
		"absolute":  {map[string]string{"host": "/"}, "host"},
		"climbs":    {map[string]string{"up": "../.."}, "up"},
		"one level": {map[string]string{"m": "../outside"}, "m"},
		// Each link passes a lexical check; only the kernel's walk escapes.
		"chained": {map[string]string{"d/e/sl": "../..", "d/e/s": "sl/../../.."}, "d/e/s"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRemoteRepo(t)
			for link, target := range c.links {
				p := filepath.Join(r.work, link)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			r.commit("main", "a.txt", "a", "with links")
			dir := t.TempDir()
			_, err := (Git{}).Fetch(context.Background(), gitSource(r.url, "main"), dir, &bytes.Buffer{})
			if !errors.Is(err, ErrUnsafeCheckout) {
				t.Fatalf("Fetch error = %v, want ErrUnsafeCheckout", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "src", c.escaping)); !os.IsNotExist(err) {
				t.Fatalf("escaping link %q left in src: %v", c.escaping, err)
			}
		})
	}
}

func TestFetchKeepsSymlinksInsideTree(t *testing.T) {
	r := newRemoteRepo(t)
	if err := os.MkdirAll(filepath.Join(r.work, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"d/a": "../a.txt", "dangling": "missing.txt", "self": "."} {
		if err := os.Symlink(target, filepath.Join(r.work, link)); err != nil {
			t.Fatal(err)
		}
	}
	r.commit("main", "a.txt", "a", "with inside links")
	dir := t.TempDir()
	if _, err := (Git{}).Fetch(context.Background(), gitSource(r.url, "main"), dir, &bytes.Buffer{}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src", "d", "a")); err != nil || string(b) != "a" {
		t.Fatalf("read through inside link = %q, %v", b, err)
	}
}

// serveFilters lets the bare remote answer partial fetches, as GitHub does.
// Without it git falls back to a full fetch and a test proves nothing.
func (r *remoteRepo) serveFilters() {
	bare := strings.TrimPrefix(r.url, "file://")
	runGit(r.t, bare, "config", "uploadpack.allowFilter", "true")
	runGit(r.t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
}

func TestFetchSparsePaths(t *testing.T) {
	r := newRemoteRepo(t)
	r.serveFilters()
	r.commit("main", "root.txt", "root", "root")
	for _, f := range []string{"app/a.txt", "big/b.txt"} {
		if err := os.MkdirAll(filepath.Join(r.work, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.commit("main", "app/a.txt", "a", "app")
	r.commit("main", "big/b.txt", "b", "big")
	bigBlob := runGit(t, r.work, "rev-parse", "HEAD:big/b.txt")

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	fetch := func(paths ...string) {
		t.Helper()
		s := gitSource(r.url, "main")
		s.SparsePaths = paths
		var out bytes.Buffer
		if _, err := (Git{}).Fetch(context.Background(), s, dir, &out); err != nil {
			t.Fatalf("Fetch(%q): %v\n%s", paths, err, out.String())
		}
	}

	fetch("app")
	if !exists(filepath.Join(src, "app", "a.txt")) || !exists(filepath.Join(src, "root.txt")) {
		t.Fatal("sparse checkout is missing app/a.txt or a root file")
	}
	if exists(filepath.Join(src, "big")) {
		t.Fatal("big/ was checked out outside the paths")
	}
	missing := runGit(t, src, "rev-list", "--objects", "--missing=print", "HEAD")
	if !strings.Contains(missing, "?"+bigBlob) {
		t.Fatalf("big/b.txt was downloaded; rev-list:\n%s", missing)
	}

	fetch()
	if !exists(filepath.Join(src, "big", "b.txt")) {
		t.Fatal("no paths did not check out the whole repository")
	}

	fetch("app")
	if exists(filepath.Join(src, "big")) {
		t.Fatal("narrowing again left big/ in place")
	}
}

func TestFetchSparsePathsSkipsSubmodulesOutside(t *testing.T) {
	sub := newRemoteRepo(t)
	sub.commit("main", "lib.txt", "lib", "lib")
	r := newRemoteRepo(t)
	r.serveFilters()
	if err := os.MkdirAll(filepath.Join(r.work, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.commit("main", "app/a.txt", "a", "a")
	runGit(t, r.work, "submodule", "add", sub.url, "app/inside")
	runGit(t, r.work, "submodule", "add", sub.url, "outside")
	runGit(t, r.work, "commit", "-m", "add submodules")
	runGit(t, r.work, "push", "origin", "main")

	dir := t.TempDir()
	s := gitSource(r.url, "main")
	s.Submodules = true
	s.SparsePaths = []string{"app"}
	var out bytes.Buffer
	if _, err := (Git{}).Fetch(context.Background(), s, dir, &out); err != nil {
		t.Fatalf("Fetch: %v\n%s", err, out.String())
	}
	if !exists(filepath.Join(dir, "src", "app", "inside", "lib.txt")) {
		t.Fatal("submodule inside the paths is missing")
	}
	if exists(filepath.Join(dir, "src", "outside", "lib.txt")) {
		t.Fatal("submodule outside the paths was checked out")
	}
}
