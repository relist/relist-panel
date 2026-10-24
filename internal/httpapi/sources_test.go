package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

const foreignSrcMessage = "src/ already exists in this Compose's directory, move it first"

func (f deploymentsFixture) sourcePath() string { return f.path() + "/source" }

func (f deploymentsFixture) dir(t *testing.T) string {
	t.Helper()
	target, err := composes.New(f.server.db, f.server.runner, f.server.dataDir).Target(context.Background(), f.compose.ID)
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	return composes.ComposeDir(target.Request)
}

func (f deploymentsFixture) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	response := f.server.request(t, method, path, body)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return response.StatusCode, out
}

func (f deploymentsFixture) putGit(t *testing.T, repoURL, branch string) (int, map[string]any) {
	t.Helper()
	return f.do(t, http.MethodPut, f.sourcePath(), map[string]any{"kind": "git", "repoUrl": repoURL, "branch": branch, "submodules": false})
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func (f deploymentsFixture) uploadRequest(t *testing.T, name string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "http://relist-panel.test"+f.sourcePath()+"/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func (f deploymentsFixture) upload(t *testing.T, name string, data []byte) (int, map[string]any) {
	t.Helper()
	response := f.server.serve(f.uploadRequest(t, name, data))
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return response.StatusCode, out
}

func newSourceFixture(t *testing.T) deploymentsFixture {
	t.Helper()
	return newDeploymentsFixture(t, newDeploymentsServer(t), "Src", "web", plainYAML)
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func TestSourcePutGitGeneratesKeyAndKeepsItOnSecondPut(t *testing.T) {
	f := newSourceFixture(t)
	status, first := f.putGit(t, "git@github.com:acme/shop.git", "")
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %v, want 200", status, first)
	}
	key, _ := first["publicKey"].(string)
	if !strings.HasPrefix(key, "ssh-ed25519 ") {
		t.Fatalf("publicKey = %q, want ssh-ed25519", key)
	}
	for k := range first {
		if strings.Contains(strings.ToLower(k), "private") {
			t.Fatalf("response carries %q", k)
		}
	}
	if first["kind"] != "git" || first["zip"] != nil || first["lastTest"] != nil {
		t.Fatalf("response = %v", first)
	}
	_, second := f.putGit(t, "git@github.com:acme/shop.git", "main")
	if second["publicKey"] != key || second["branch"] != "main" {
		t.Fatalf("second PUT = %v, want same key and branch main", second)
	}
	status, got := f.do(t, http.MethodGet, f.sourcePath(), nil)
	if status != http.StatusOK || got["publicKey"] != key {
		t.Fatalf("GET = %d %v", status, got)
	}
}

func TestSourcePutRejectsBadInput(t *testing.T) {
	f := newSourceFixture(t)
	if status, _ := f.putGit(t, "https://github.com/acme/shop.git", ""); status != http.StatusBadRequest {
		t.Fatalf("https url = %d, want 400", status)
	}
	if status, _ := f.putGit(t, "git@github.com:acme/shop.git", "-x"); status != http.StatusBadRequest {
		t.Fatalf("branch -x = %d, want 400", status)
	}
	if status, _ := f.do(t, http.MethodPut, f.sourcePath(), map[string]any{"kind": "zip"}); status != http.StatusBadRequest {
		t.Fatalf("kind zip = %d, want 400", status)
	}
}

func TestSourceGetWithoutSourceIs404(t *testing.T) {
	f := newSourceFixture(t)
	if status, _ := f.do(t, http.MethodGet, f.sourcePath(), nil); status != http.StatusNotFound {
		t.Fatalf("GET = %d, want 404", status)
	}
}

func TestSourceRegenerateKeyChangesIt(t *testing.T) {
	f := newSourceFixture(t)
	_, first := f.putGit(t, "git@github.com:acme/shop.git", "")
	status, regen := f.do(t, http.MethodPost, f.sourcePath()+"/key", nil)
	if status != http.StatusOK {
		t.Fatalf("regenerate = %d %v", status, regen)
	}
	key, _ := regen["publicKey"].(string)
	if !strings.HasPrefix(key, "ssh-ed25519 ") || key == first["publicKey"] {
		t.Fatalf("regenerated key = %q, first = %v", key, first["publicKey"])
	}
	_, got := f.do(t, http.MethodGet, f.sourcePath(), nil)
	if got["publicKey"] != key {
		t.Fatalf("stored key = %v, want %q", got["publicKey"], key)
	}
}

func TestSourceRegenerateKeyOnZipIs400(t *testing.T) {
	f := newSourceFixture(t)
	f.upload(t, "a.zip", zipBytes(t, map[string]string{"Dockerfile": "FROM scratch\n"}))
	if status, _ := f.do(t, http.MethodPost, f.sourcePath()+"/key", nil); status != http.StatusBadRequest {
		t.Fatalf("regenerate on zip = %d, want 400", status)
	}
}

func TestSourcePutRefusesAnUnmarkedSrc(t *testing.T) {
	f := newSourceFixture(t)
	src := filepath.Join(f.dir(t), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	status, body := f.putGit(t, "git@github.com:acme/shop.git", "")
	if status != http.StatusConflict || body["error"] != foreignSrcMessage {
		t.Fatalf("PUT = %d %v, want 409 %q", status, body, foreignSrcMessage)
	}
}

func TestSourceUploadCreatesAZipSource(t *testing.T) {
	f := newSourceFixture(t)
	status, body := f.upload(t, "site.zip", zipBytes(t, map[string]string{"Dockerfile": "FROM scratch\n", "app/main.go": "package main\n"}))
	if status != http.StatusOK {
		t.Fatalf("upload = %d %v", status, body)
	}
	zipInfo, _ := body["zip"].(map[string]any)
	if body["kind"] != "zip" || zipInfo["name"] != "site.zip" || zipInfo["files"] != float64(2) || zipInfo["size"] == nil || zipInfo["uploadedAt"] == nil {
		t.Fatalf("response = %v", body)
	}
	if body["repoUrl"] != "" || body["branch"] != "" || body["publicKey"] != "" {
		t.Fatalf("zip source carries git fields: %v", body)
	}
	dir := f.dir(t)
	if mustRead(t, filepath.Join(dir, "src", "Dockerfile")) != "FROM scratch\n" {
		t.Fatal("src/Dockerfile missing")
	}
	if exists(filepath.Join(dir, ".src.old")) {
		t.Fatal(".src.old left behind")
	}
	noStagingLeft(t, dir)
	if m, _ := filepath.Glob(filepath.Join(dir, ".upload-*.zip")); len(m) != 0 {
		t.Fatalf("temp upload left behind: %v", m)
	}
}

func TestSourceUploadWithUnsafeEntryKeepsTheOldSrc(t *testing.T) {
	f := newSourceFixture(t)
	f.upload(t, "ok.zip", zipBytes(t, map[string]string{"Dockerfile": "FROM scratch\n"}))
	status, body := f.upload(t, "bad.zip", zipBytes(t, map[string]string{"../x": "evil", "Dockerfile": "FROM busybox\n"}))
	if status != http.StatusBadRequest || body["error"] == nil || body["error"] == "" {
		t.Fatalf("upload = %d %v, want 400 with an error", status, body)
	}
	dir := f.dir(t)
	if mustRead(t, filepath.Join(dir, "src", "Dockerfile")) != "FROM scratch\n" {
		t.Fatal("old src changed")
	}
	noStagingLeft(t, dir)
	if m, _ := filepath.Glob(filepath.Join(dir, ".upload-*.zip")); len(m) != 0 {
		t.Fatalf("temp upload left behind: %v", m)
	}
	_, got := f.do(t, http.MethodGet, f.sourcePath(), nil)
	if z, _ := got["zip"].(map[string]any); z["name"] != "ok.zip" {
		t.Fatalf("row changed: %v", got)
	}
}

func TestSourceUploadNotAZipIs400(t *testing.T) {
	f := newSourceFixture(t)
	if status, _ := f.upload(t, "x.zip", []byte("not a zip")); status != http.StatusBadRequest {
		t.Fatalf("upload = %d, want 400", status)
	}
}

func TestSourceUploadRefusesAForeignSrc(t *testing.T) {
	f := newSourceFixture(t)
	if err := os.MkdirAll(filepath.Join(f.dir(t), "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, body := f.upload(t, "a.zip", zipBytes(t, map[string]string{"Dockerfile": "x"}))
	if status != http.StatusConflict || body["error"] != foreignSrcMessage {
		t.Fatalf("upload = %d %v, want 409", status, body)
	}
	noStagingLeft(t, f.dir(t))
}

func TestSourceUploadSwitchesAGitSourceToZipAndBack(t *testing.T) {
	f := newSourceFixture(t)
	f.putGit(t, "git@github.com:acme/shop.git", "main")
	status, body := f.upload(t, "a.zip", zipBytes(t, map[string]string{"Dockerfile": "x"}))
	if status != http.StatusOK || body["kind"] != "zip" || body["publicKey"] != "" {
		t.Fatalf("upload = %d %v", status, body)
	}
	status, body = f.putGit(t, "git@github.com:acme/shop.git", "")
	if status != http.StatusOK || body["kind"] != "git" || !strings.HasPrefix(body["publicKey"].(string), "ssh-ed25519 ") || body["zip"] != nil {
		t.Fatalf("PUT git over zip = %d %v", status, body)
	}
}

func TestSourceDockerfilesListsTheDetectedOnes(t *testing.T) {
	f := newSourceFixture(t)
	f.upload(t, "a.zip", zipBytes(t, map[string]string{"api/Dockerfile": "FROM scratch\n", "README": "hi"}))
	response := f.server.request(t, http.MethodGet, f.sourcePath()+"/dockerfiles", nil)
	defer response.Body.Close()
	var list []struct{ Path, Service, Context string }
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("dockerfiles = %d %v", response.StatusCode, err)
	}
	if len(list) != 1 || list[0].Path != "api/Dockerfile" || list[0].Service != "api" || list[0].Context != "./src/api" {
		t.Fatalf("list = %+v", list)
	}
}

func TestSourceDockerfilesWithoutSrcIsAnEmptyArray(t *testing.T) {
	f := newSourceFixture(t)
	response := f.server.request(t, http.MethodGet, f.sourcePath()+"/dockerfiles", nil)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("dockerfiles = %d %s", response.StatusCode, raw)
	}
}

func TestSourceKnownHostsResetRemovesTheFile(t *testing.T) {
	f := newSourceFixture(t)
	path := filepath.Join(f.dir(t), ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("host key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.do(t, http.MethodPost, f.sourcePath()+"/known-hosts/reset", nil); status != http.StatusNoContent {
		t.Fatalf("reset = %d, want 204", status)
	}
	if exists(path) {
		t.Fatal("known_hosts still there")
	}
	if status, _ := f.do(t, http.MethodPost, f.sourcePath()+"/known-hosts/reset", nil); status != http.StatusNoContent {
		t.Fatalf("second reset = %d, want 204", status)
	}
}

func TestSourceDeleteRemovesRowAndSrcButKeepsDeployments(t *testing.T) {
	f := newSourceFixture(t)
	f.upload(t, "a.zip", zipBytes(t, map[string]string{"Dockerfile": "x"}))
	if _, err := f.server.db.Exec(`INSERT INTO deployments (compose_id, number, mode, status, queued_at) VALUES (?, 1, 'build', 'succeeded', 1)`, f.compose.ID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	if status, _ := f.do(t, http.MethodDelete, f.sourcePath(), nil); status != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", status)
	}
	if exists(filepath.Join(f.dir(t), "src")) {
		t.Fatal("src/ still there")
	}
	if status, _ := f.do(t, http.MethodGet, f.sourcePath(), nil); status != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", status)
	}
	var n int
	if err := f.server.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE compose_id = ?`, f.compose.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("deployments = %d (%v), want 1", n, err)
	}
}

func TestSourceDeleteLeavesAForeignSrcAlone(t *testing.T) {
	f := newSourceFixture(t)
	f.putGit(t, "git@github.com:acme/shop.git", "")
	src := filepath.Join(f.dir(t), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.do(t, http.MethodDelete, f.sourcePath(), nil); status != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", status)
	}
	if !exists(src) {
		t.Fatal("foreign src/ was removed")
	}
}

// bareRepo makes a bare repository with one commit on main and returns its file:// URL and commit SHA.
func bareRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(root, "init", "--bare", "-b", "main", bare)
	run(root, "init", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", ".")
	run(work, "commit", "-m", "init")
	run(work, "push", bare, "main")
	return "file://" + bare, run(work, "rev-parse", "HEAD")
}

func (f deploymentsFixture) saveGitRow(t *testing.T, url, branch string) {
	t.Helper()
	priv, pub, err := sources.GenerateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	err = sources.NewStore(f.server.db).Save(context.Background(), sources.Source{ComposeID: f.compose.ID, Kind: sources.KindGit, RepoURL: url, Branch: branch, PrivateKey: priv, PublicKey: pub})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestSourceTestListsBranchesAndStoresHeadSHA(t *testing.T) {
	f := newSourceFixture(t)
	url, sha := bareRepo(t)
	f.saveGitRow(t, url, "main")
	status, body := f.do(t, http.MethodPost, f.sourcePath()+"/test", nil)
	branches, _ := body["branches"].([]any)
	if status != http.StatusOK || body["ok"] != true || len(branches) != 1 || branches[0] != "main" {
		t.Fatalf("test = %d %v", status, body)
	}
	_, got := f.do(t, http.MethodGet, f.sourcePath(), nil)
	lt, _ := got["lastTest"].(map[string]any)
	if got["headSha"] != sha || lt["ok"] != true || lt["at"] == nil {
		t.Fatalf("GET after test = %v, want headSha %s", got, sha)
	}
}

func TestSourceTestOfAMissingRepositoryIsOkFalse(t *testing.T) {
	f := newSourceFixture(t)
	f.saveGitRow(t, "file://"+filepath.Join(t.TempDir(), "nope.git"), "main")
	status, body := f.do(t, http.MethodPost, f.sourcePath()+"/test", nil)
	branches, isArray := body["branches"].([]any)
	if status != http.StatusOK || body["ok"] != false || body["error"] == "" || !isArray || len(branches) != 0 {
		t.Fatalf("test = %d %v", status, body)
	}
	_, got := f.do(t, http.MethodGet, f.sourcePath(), nil)
	if lt, _ := got["lastTest"].(map[string]any); lt["ok"] != false {
		t.Fatalf("lastTest = %v, want ok false", got["lastTest"])
	}
}

func TestSourceTestWithoutAGitSourceIs404(t *testing.T) {
	f := newSourceFixture(t)
	if status, _ := f.do(t, http.MethodPost, f.sourcePath()+"/test", nil); status != http.StatusNotFound {
		t.Fatalf("no source = %d, want 404", status)
	}
	f.upload(t, "a.zip", zipBytes(t, map[string]string{"Dockerfile": "x"}))
	if status, _ := f.do(t, http.MethodPost, f.sourcePath()+"/test", nil); status != http.StatusNotFound {
		t.Fatalf("zip source = %d, want 404", status)
	}
}

func TestSourceTestWithABodyURLValidatesAndDoesNotSaveIt(t *testing.T) {
	f := newSourceFixture(t)
	f.saveGitRow(t, "git@github.com:acme/shop.git", "")
	if status, _ := f.do(t, http.MethodPost, f.sourcePath()+"/test", map[string]string{"repoUrl": "https://x/y"}); status != http.StatusBadRequest {
		t.Fatalf("bad url = %d, want 400", status)
	}
}

func TestSourceTestAndUploadAnswerWhileADeployHoldsTheProjectGate(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	f := newDeploymentsFixture(t, server, "Gate", "web", plainYAML)
	url, _ := bareRepo(t)
	f.saveGitRow(t, url, "main")

	f.post(t, map[string]any{"mode": "skip-build"})
	waitSignal(t, runner.upStarted, "the deploy to reach Up")
	released := false
	release := func() {
		if !released {
			released = true
			runner.release()
		}
	}
	defer release()

	within(t, 5*time.Second, "source test", func() {
		status, body := f.do(t, http.MethodPost, f.sourcePath()+"/test", nil)
		if status != http.StatusOK || body["ok"] != true {
			t.Errorf("test during Deploy = %d %v", status, body)
		}
	})

	// The upload streams and extracts without the gate, then waits for it to swap.
	type result struct {
		status int
		body   map[string]any
	}
	done := make(chan result, 1)
	go func() {
		status, body := f.upload(t, "a.zip", zipBytes(t, map[string]string{"Dockerfile": "x"}))
		done <- result{status, body}
	}()
	staged := filepath.Join(f.dir(t), ".src.new-*", "Dockerfile")
	deadline := time.Now().Add(5 * time.Second)
	for m, _ := filepath.Glob(staged); len(m) == 0; m, _ = filepath.Glob(staged) {
		if time.Now().After(deadline) {
			t.Fatal("upload did not extract while the Deploy held the gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case r := <-done:
		t.Fatalf("upload swapped src/ under the held gate: %d %v", r.status, r.body)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case r := <-done:
		if r.status != http.StatusOK {
			t.Fatalf("upload = %d %v, want 200", r.status, r.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish after the gate was released")
	}
}

func noStagingLeft(t *testing.T, dir string) {
	t.Helper()
	for _, pattern := range []string{".src.new-*", ".upload-*.zip"} {
		if m, _ := filepath.Glob(filepath.Join(dir, pattern)); len(m) != 0 {
			t.Fatalf("%s left behind: %v", pattern, m)
		}
	}
}

func TestSourceConcurrentUploadsNeverMixTrees(t *testing.T) {
	f := newSourceFixture(t)
	zips := []map[string]string{
		{"Dockerfile": "FROM a\n", "a-only": "a"},
		{"Dockerfile": "FROM b\n", "b-only": "b"},
	}
	codes := make([]int, len(zips))
	var wg sync.WaitGroup
	for i := range zips {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = f.upload(t, "z.zip", zipBytes(t, zips[i]))
		}()
	}
	wg.Wait()
	wins := 0
	for _, c := range codes {
		if c != http.StatusOK && c != http.StatusConflict {
			t.Fatalf("codes = %v, want 200 or 409", codes)
		}
		if c == http.StatusOK {
			wins++
		}
	}
	if wins == 0 {
		t.Fatalf("codes = %v, want at least one 200", codes)
	}
	src := filepath.Join(f.dir(t), "src")
	_, hasA := os.Stat(filepath.Join(src, "a-only"))
	_, hasB := os.Stat(filepath.Join(src, "b-only"))
	if (hasA == nil) == (hasB == nil) {
		t.Fatalf("src/ holds a mix or nothing (a=%v b=%v)", hasA, hasB)
	}
	noStagingLeft(t, f.dir(t))
}

func TestSourceUploadWithoutAFileNameDefaultsIt(t *testing.T) {
	f := newSourceFixture(t)
	status, body := f.upload(t, "", zipBytes(t, map[string]string{"Dockerfile": "x"}))
	z, _ := body["zip"].(map[string]any)
	if status != http.StatusOK || z["name"] != "upload.zip" {
		t.Fatalf("upload = %d %v, want name upload.zip", status, body)
	}
}

func TestPreviewIncludesVolumeWarnings(t *testing.T) {
	f := newSourceFixture(t)
	if status, body := f.upload(t, "site.zip", zipBytes(t, map[string]string{"Dockerfile": "FROM scratch\n"})); status != http.StatusOK {
		t.Fatalf("upload = %d %v", status, body)
	}
	draft := "services:\n  web:\n    image: nginx\n    volumes:\n      - ./src:/app\n"
	status, body := f.do(t, http.MethodPost, f.path()+"/preview", map[string]any{"yamlContent": draft})
	if status != http.StatusOK {
		t.Fatalf("preview = %d %v", status, body)
	}
	want := `Service "web" mounts ./src writable at /app: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.`
	if warnings, _ := body["warnings"].([]any); len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %v, want [%q]", body["warnings"], want)
	}
}
