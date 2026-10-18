package migration

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/config"
)

type sourceServer struct {
	*sourceFixture
	mux *http.ServeMux
}

func newSourceServer(t *testing.T) *sourceServer {
	t.Helper()
	f := newSourceFixture(t, httpsDomain)
	f.src = NewSource(SourceDeps{Store: f.store, Builder: f.builder(), Queue: f.queue, Docker: f.docker,
		Status: statusFromDocker{f.docker}, Composes: f.targets, Domains: fakeDomains{httpsDomain},
		DB: f.db, DataDir: f.dataDir, Install: config.InstallSettings{Domain: "panel.old.example.com"}})
	mux := http.NewServeMux()
	f.src.RegisterRoutes(mux)
	return &sourceServer{sourceFixture: f, mux: mux}
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
	rec := s.do(t, "POST", "/api/migration/tokens", "", map[string]string{"lifetime": "24h"})
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
	rec := s.do(t, "POST", "/api/migration/tokens", "", map[string]string{"lifetime": "24h"})
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
	if rec := s.do(t, "POST", "/api/migration/tokens", "", map[string]string{"lifetime": "2h"}); rec.Code != http.StatusBadRequest ||
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

func TestResumeRequiresConfirm(t *testing.T) {
	s := newSourceServer(t)
	key := s.claim(t)
	s.startReady(t, key)
	if rec := s.do(t, "POST", "/api/migration/resume", "", map[string]string{"confirm": "resume"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "type RESUME to confirm") {
		t.Fatalf("resume without confirm = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "POST", "/api/migration/resume", "", map[string]string{"confirm": "RESUME"}); rec.Code != http.StatusNoContent {
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
