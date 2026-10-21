package migration

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/config"
)

type sourceServer struct {
	*sourceFixture
	mux     *http.ServeMux
	control *http.ServeMux
}

func newSourceServer(t *testing.T) *sourceServer {
	t.Helper()
	f := newSourceFixture(t, httpsDomain)
	f.src = NewSource(SourceDeps{Store: f.store, Builder: f.builder(), Queue: f.queue, Docker: f.docker,
		Status: statusFromDocker{f.docker}, Composes: f.targets, Domains: fakeDomains{httpsDomain},
		DB: f.db, DataDir: f.dataDir, Install: config.InstallSettings{Domain: "panel.old.example.com"}})
	mux, control := http.NewServeMux(), http.NewServeMux()
	f.src.RegisterRoutes(mux)
	f.src.RegisterControlRoutes(control)
	return &sourceServer{sourceFixture: f, mux: mux, control: control}
}

// ctl serves a request on the control socket's mux, as the terminal does.
func (s *sourceServer) ctl(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	rec := httptest.NewRecorder()
	s.control.ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec
}

func (s *sourceServer) do(t *testing.T, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func (s *sourceServer) claim(t *testing.T) string {
	t.Helper()
	rec := s.ctl(t, "POST", "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token = %d %s", rec.Code, rec.Body)
	}
	var tok struct{ Token string }
	json.Unmarshal(rec.Body.Bytes(), &tok)
	rec = s.do(t, "POST", "/api/migration/claim", "", map[string]string{"token": tok.Token, "claimedBy": "https://new.example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}
	var out struct{ SessionKey string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.SessionKey
}

// startReady posts start until the manifest comes back.
func (s *sourceServer) startReady(t *testing.T, key string) Manifest {
	t.Helper()
	var m Manifest
	eventuallyTrue(t, "start to return the manifest", func() bool {
		rec := s.do(t, "POST", "/api/migration/start", key, map[string]any{"hostPaths": []string{}})
		if rec.Code == http.StatusOK {
			json.Unmarshal(rec.Body.Bytes(), &m)
			return true
		}
		if rec.Code != http.StatusAccepted {
			t.Fatalf("start = %d %s", rec.Code, rec.Body)
		}
		return false
	})
	return m
}

func TestClaimRoute(t *testing.T) {
	s := newSourceServer(t)
	rec := s.ctl(t, "POST", "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	var tok struct {
		Token     string
		ExpiresAt time.Time
	}
	json.Unmarshal(rec.Body.Bytes(), &tok)
	if rec.Code != http.StatusCreated || tok.Token == "" || tok.ExpiresAt.IsZero() {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "POST", "/api/migration/claim", "", map[string]string{"token": "nope"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", rec.Code)
	}
	if rec := s.do(t, "POST", "/api/migration/claim", "", map[string]string{"token": tok.Token, "claimedBy": "x"}); rec.Code != http.StatusOK {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "POST", "/api/migration/claim", "", map[string]string{"token": tok.Token}); rec.Code != http.StatusConflict {
		t.Fatalf("second claim = %d", rec.Code)
	}
	for i := 0; i < 5; i++ {
		s.do(t, "POST", "/api/migration/claim", "", map[string]string{"token": "nope"})
	}
	if rec := s.do(t, "POST", "/api/migration/claim", "", map[string]string{"token": tok.Token}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked claim = %d", rec.Code)
	}
	if rec := s.ctl(t, "POST", "/api/migration/tokens", map[string]string{"lifetime": "2h"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "lifetime must be 1h, 24h or 168h") {
		t.Fatalf("bad lifetime = %d %s", rec.Code, rec.Body)
	}
}

func TestSessionRoutesRejectMissingOrWrongBearer(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/migration/preflight"}, {"POST", "/api/migration/start"}, {"GET", "/api/migration/items/database"},
		{"GET", "/api/migration/items/database/digest"}, {"POST", "/api/migration/abort"}, {"POST", "/api/migration/finish"},
	} {
		for _, bearer := range []string{"", key + "x"} {
			if rec := s.do(t, r.method, r.path, bearer, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with %q = %d, want 401", r.method, r.path, bearer, rec.Code)
			}
		}
	}
	rec := s.do(t, "GET", "/api/migration/preflight", key, nil)
	var info SourceInfo
	json.Unmarshal(rec.Body.Bytes(), &info)
	if rec.Code != http.StatusOK || info.Protocol != ProtocolVersion || info.DataDir != s.dataDir || info.Install.Domain != "panel.old.example.com" {
		t.Fatalf("preflight = %d %+v", rec.Code, info)
	}
}

func TestPreflightCountsQueuedDeployments(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	if _, err := s.db.Exec(`INSERT INTO deployments (compose_id, number, mode, status, queued_at) VALUES (?, 1, 'deploy', 'queued', 1), (?, 1, 'deploy', 'succeeded', 1)`, s.a, s.b); err != nil {
		t.Fatal(err)
	}
	var info SourceInfo
	json.Unmarshal(s.do(t, "GET", "/api/migration/preflight", key, nil).Body.Bytes(), &info)
	if info.QueuedDeployments != 1 {
		t.Fatalf("queued deployments = %d, want 1", info.QueuedDeployments)
	}
}

func TestStartReturns202ThenManifest(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.queue.waitBlock = make(chan struct{})
	rec := s.do(t, "POST", "/api/migration/start", key, map[string]any{"hostPaths": []string{}})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	close(s.queue.waitBlock)
	m := s.startReady(t, key)
	if len(m.Items) == 0 || m.Items[0].ID != "database" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestItemStreamRefusedBeforeReady(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	if rec := s.do(t, "GET", "/api/migration/items/database", key, nil); rec.Code != http.StatusConflict {
		t.Fatalf("item before start = %d", rec.Code)
	}
}

func TestItemStreamDigestMatchesBody(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	if rec := s.do(t, "GET", "/api/migration/items/data-dir/digest", key, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("digest before a stream = %d", rec.Code)
	}
	for _, id := range []string{"database", "data-dir"} {
		rec := s.do(t, "GET", "/api/migration/items/"+id, key, nil)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("%s = %d (%d bytes)", id, rec.Code, rec.Body.Len())
		}
		sum := sha256.Sum256(rec.Body.Bytes())
		rec2 := s.do(t, "GET", "/api/migration/items/"+id+"/digest", key, nil)
		var d struct{ Sha256 string }
		json.Unmarshal(rec2.Body.Bytes(), &d)
		if d.Sha256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("%s digest = %q, want %x", id, d.Sha256, sum)
		}
	}
	if rec := s.do(t, "GET", "/api/migration/items/nope", key, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown item = %d", rec.Code)
	}
}

func TestDataDirItemSkipsDatabaseAndMigrationDirs(t *testing.T) {
	s := newSourceServer(t)
	for _, p := range []string{"panel.db-journal", ".migration/state.json", ".traefik/acme.json"} {
		os.MkdirAll(filepath.Dir(filepath.Join(s.dataDir, p)), 0o700)
		os.WriteFile(filepath.Join(s.dataDir, p), []byte("x"), 0o600)
	}
	os.WriteFile(filepath.Join(s.dataDir, "panel.db"), []byte("db"), 0o600)
	key := s.claim(t)
	s.startReady(t, key)
	rec := s.do(t, "GET", "/api/migration/items/data-dir", key, nil)
	tr := tar.NewReader(rec.Body)
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, ".traefik/acme.json") || strings.Contains(joined, "panel.db") || strings.Contains(joined, ".migration/") {
		t.Fatalf("data-dir entries = %q", names)
	}
}

func TestDatabaseItemIsAConsistentCopy(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	rec := s.do(t, "GET", "/api/migration/items/database", key, nil)
	path := filepath.Join(t.TempDir(), "copy.db")
	os.WriteFile(path, rec.Body.Bytes(), 0o600)
	copyDB := openDBAt(t, path)
	var n int
	if err := copyDB.QueryRow("SELECT COUNT(*) FROM composes").Scan(&n); err != nil || n != 2 {
		t.Fatalf("copied composes = %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, ".migration-out")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(s.dataDir, ".migration-out"))
		if len(entries) != 0 {
			t.Fatalf("the VACUUM copy was left behind: %v", entries)
		}
	}
}

func TestConcurrentDatabaseStreamsDoNotCollide(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			errs <- s.src.StreamItem(context.Background(), "database", &buf)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent database stream failed: %v", err)
		}
	}
}

func TestResumeRequiresConfirm(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	if rec := s.ctl(t, "POST", "/api/migration/resume", map[string]string{"confirm": "resume"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "type RESUME to confirm") {
		t.Fatalf("resume without confirm = %d %s", rec.Code, rec.Body)
	}
	if rec := s.ctl(t, "POST", "/api/migration/resume", map[string]string{"confirm": "RESUME"}); rec.Code != http.StatusNoContent {
		t.Fatalf("resume = %d %s", rec.Code, rec.Body)
	}
	if step, _, _ := s.store.Lock(context.Background()); step != StepNone {
		t.Fatalf("step after resume = %q", step)
	}
}

func TestStatusAndFinishRoutes(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	if rec := s.do(t, "POST", "/api/migration/finish", key, map[string]string{"targetUrl": "https://new.example.com"}); rec.Code != http.StatusNoContent {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "GET", "/api/migration/items/database", key, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an item after finish = %d, want 401: the session ends with finish", rec.Code)
	}
	if rec := s.do(t, "POST", "/api/migration/abort", key, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("abort after finish = %d, want 401", rec.Code)
	}
	rec := s.do(t, "GET", "/api/migration/status", "", nil)
	var st struct {
		Step, ClaimedBy, TargetURL string
	}
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Step != "migrated" || st.ClaimedBy != "https://new.example.com" || st.TargetURL != "https://new.example.com" {
		t.Fatalf("status = %s", rec.Body)
	}
}

func TestFinishRefusesATargetURLThatIsNotHTTP(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,x", "https://", "new.example.com", ""} {
		if rec := s.do(t, "POST", "/api/migration/finish", key, map[string]string{"targetUrl": bad}); rec.Code != http.StatusBadRequest {
			t.Fatalf("finish with %q = %d %s, want 400", bad, rec.Code, rec.Body)
		}
	}
	if step, _, _ := s.store.Lock(context.Background()); step != StepReady {
		t.Fatalf("step = %q, want ready: a refused finish changes nothing", step)
	}
}

func TestItemsWaitForTheLockToSettleAfterARestart(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	// The old host reboots: restart: always brings back a Compose that was
	// stopped before the lock, so it is not in RunningComposeIDs.
	s.docker.Running[fmt.Sprintf("relist-c%d", s.b)] = true
	rebooted := NewSource(SourceDeps{Store: s.store, Builder: s.builder(), Queue: s.queue, Docker: s.docker,
		Status: statusFromDocker{s.docker}, Composes: s.targets, Domains: fakeDomains{httpsDomain}, DB: s.db, DataDir: s.dataDir})
	mux := http.NewServeMux()
	rebooted.RegisterRoutes(mux)
	s.mux = mux
	if rec := s.do(t, "GET", "/api/migration/items/data-dir", key, nil); rec.Code != http.StatusConflict {
		t.Fatalf("item before ResumeLock = %d, want 409", rec.Code)
	}
	if err := rebooted.ResumeLock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.docker.Running[fmt.Sprintf("relist-c%d", s.b)] {
		t.Fatal("ResumeLock left a Compose running that was not recorded")
	}
	if rec := s.do(t, "GET", "/api/migration/items/data-dir", key, nil); rec.Code != http.StatusOK {
		t.Fatalf("item after ResumeLock = %d", rec.Code)
	}
}

// pausingWriter signals after its first write and blocks until released.
type pausingWriter struct {
	first   chan struct{}
	release chan struct{}
	once    sync.Once
	err     error
}

func (w *pausingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	w.once.Do(func() {
		close(w.first)
		<-w.release
	})
	return len(p), nil
}

func TestActivityFollowsAStream(t *testing.T) {
	s := newSourceServer(t)
	s.startReady(t, s.claim(t))
	w := &pausingWriter{first: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- s.src.StreamItem(context.Background(), "database", w) }()
	<-w.first
	if a := s.src.Activity(); a.Item != "database" || a.Bytes <= 0 || a.Done != 0 {
		t.Fatalf("activity mid-stream = %+v, want database with bytes and none done", a)
	}
	close(w.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a := s.src.Activity(); a.Item != "" || a.Bytes != 0 || a.Done != 1 {
		t.Fatalf("activity after the stream = %+v, want idle with one done", a)
	}
}

func TestActivityIgnoresAFailedStream(t *testing.T) {
	s := newSourceServer(t)
	s.startReady(t, s.claim(t))
	w := &pausingWriter{err: errors.New("connection reset")}
	if err := s.src.StreamItem(context.Background(), "database", w); err == nil {
		t.Fatal("the stream did not fail")
	}
	if a := s.src.Activity(); a.Item != "" || a.Done != 0 {
		t.Fatalf("activity after a failed stream = %+v", a)
	}
}

func TestActivityRouteOnlyOnTheControlMux(t *testing.T) {
	s := newSourceServer(t)
	if rec := s.do(t, "GET", "/api/migration/serve/activity", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("activity on the web mux = %d, want 404", rec.Code)
	}
	control := http.NewServeMux()
	s.src.RegisterControlRoutes(control)
	rec := httptest.NewRecorder()
	control.ServeHTTP(rec, httptest.NewRequest("GET", "/api/migration/serve/activity", nil))
	var a Activity
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &a) != nil {
		t.Fatalf("activity on the control mux = %d %s", rec.Code, rec.Body)
	}
}

func TestActivityCountsItemsNotStreams(t *testing.T) {
	s := newSourceServer(t)
	s.startReady(t, s.claim(t))
	for range 3 {
		var buf bytes.Buffer
		if err := s.src.StreamItem(context.Background(), "database", &buf); err != nil {
			t.Fatal(err)
		}
	}
	if a := s.src.Activity(); a.Done != 1 {
		t.Fatalf("Done after three streams of one item = %d, want 1", a.Done)
	}
}

func TestTokenAndResumeRoutesOnlyOnTheControlMux(t *testing.T) {
	s := newSourceServer(t)
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/migration/tokens"},
		{"DELETE", "/api/migration/tokens/current"},
		{"POST", "/api/migration/resume"},
	} {
		if rec := s.do(t, r.method, r.path, "", map[string]string{"lifetime": "24h", "confirm": "RESUME"}); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s on the web mux = %d, want 404", r.method, r.path, rec.Code)
		}
	}
	if rec := s.ctl(t, "POST", "/api/migration/tokens", map[string]string{"lifetime": "24h"}); rec.Code != http.StatusCreated {
		t.Fatalf("token over the control mux = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "GET", "/api/migration/status", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("status on the web mux = %d, want 200", rec.Code)
	}
}

func TestResumeBeforeStartEndsAClaimedSession(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	if rec := s.ctl(t, "POST", "/api/migration/resume", map[string]string{"confirm": "RESUME"}); rec.Code != http.StatusNoContent {
		t.Fatalf("resume before start = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "POST", "/api/migration/start", key, map[string]any{"hostPaths": []string{}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("start with the ended session = %d, want 401", rec.Code)
	}
}
