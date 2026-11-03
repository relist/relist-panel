package panelupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func manifestJSON(version string) []byte {
	b, _ := json.Marshal(map[string]any{
		"schema": 1, "version": version, "released_at": "2026-11-20T09:00:00Z",
		"image":        "ghcr.io/relist/relist-panel@sha256:" + strings.Repeat("a", 64),
		"notes_url":    "https://example.test/notes",
		"install_file": "services: {}",
		"invariants":   []map[string]any{{"kind": "service-image", "service": "panel"}},
		"manual_steps": []map[string]string{{"id": "open-443", "text": "Open port 443."}},
	})
	return b
}

// release is a fake release server: what it serves can be changed per test.
type release struct {
	srv      *httptest.Server
	key      ed25519.PrivateKey
	manifest atomic.Value // []byte
	sig      atomic.Value // []byte
	fail     atomic.Bool
	hits     atomic.Int32
	onHit    func()
}

func newRelease(t *testing.T, priv ed25519.PrivateKey) *release {
	t.Helper()
	r := &release{key: priv}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		if r.onHit != nil {
			r.onHit()
		}
		if r.fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if strings.HasSuffix(req.URL.Path, ".sig") {
			_, _ = w.Write(r.sig.Load().([]byte))
			return
		}
		_, _ = w.Write(r.manifest.Load().([]byte))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *release) publish(m []byte) {
	r.manifest.Store(m)
	r.sig.Store(ed25519.Sign(r.key, m))
}

func newService(t *testing.T, version string) (*Service, *release) {
	t.Helper()
	pub, priv := newKey(t)
	rel := newRelease(t, priv)
	s := New(Deps{Store: NewStore(newDB(t)), Version: version, Key: pub, ManifestURL: rel.srv.URL + "/update-manifest.json"})
	return s, rel
}

func availableVersion(t *testing.T, s *Service) string {
	t.Helper()
	m, err := s.Available(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		return ""
	}
	return m.Version
}

func TestCheckOffersNewerVersion(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Available(context.Background())
	if m == nil || m.Version != "1.4.0" || len(m.Invariants) != 1 || m.Invariants[0].Kind != "service-image" || len(m.ManualSteps) != 1 {
		t.Fatalf("available = %+v", m)
	}
	st, _ := s.d.Store.Settings(context.Background())
	if st.LastCheckAt == nil || st.LastError != "" {
		t.Fatalf("settings = %+v", st)
	}
}

func TestCheckDropsBadSignature(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	_, other := newKey(t)
	rel.sig.Store(ed25519.Sign(other, rel.manifest.Load().([]byte)))
	if err := s.CheckNow(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if v := availableVersion(t, s); v != "" {
		t.Fatalf("offered %q after a bad signature", v)
	}
	st, _ := s.d.Store.Settings(context.Background())
	if !strings.Contains(st.LastError, "signature") {
		t.Fatalf("LastError = %q", st.LastError)
	}
}

func TestCheckDropsTamperedManifest(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	rel.manifest.Store(manifestJSON("9.9.9"))
	if err := s.CheckNow(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if v := availableVersion(t, s); v != "" {
		t.Fatalf("offered %q", v)
	}
}

func TestOlderOrEqualVersionIsNotOffered(t *testing.T) {
	for _, latest := range []string{"1.3.0", "1.2.9", "v1.3.0"} {
		s, rel := newService(t, "1.3.0")
		rel.publish(manifestJSON(latest))
		if err := s.CheckNow(context.Background()); err != nil {
			t.Fatalf("%s: %v", latest, err)
		}
		if v := availableVersion(t, s); v != "" {
			t.Fatalf("latest %s: offered %q", latest, v)
		}
	}
}

func TestVersionComparesNumerically(t *testing.T) {
	s, rel := newService(t, "v1.9.0")
	rel.publish(manifestJSON("1.10.0"))
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := availableVersion(t, s); v != "1.10.0" {
		t.Fatalf("offered %q, want 1.10.0", v)
	}
}

func TestDevelopmentBuildIsNeverOffered(t *testing.T) {
	s, rel := newService(t, "dev")
	rel.publish(manifestJSON("1.4.0"))
	if !s.Development() {
		t.Fatal("dev is a development build")
	}
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := availableVersion(t, s); v != "" {
		t.Fatalf("offered %q", v)
	}
	if rel.hits.Load() == 0 {
		t.Fatal("the check should still run")
	}
}

func TestFailedCheckKeepsLastGoodResult(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	rel.fail.Store(true)
	if err := s.CheckNow(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if v := availableVersion(t, s); v != "1.4.0" {
		t.Fatalf("available = %q, want 1.4.0 kept", v)
	}
	st, _ := s.d.Store.Settings(context.Background())
	if st.LastError == "" {
		t.Fatal("the error should be recorded")
	}
	// A later good check clears the error.
	rel.fail.Store(false)
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ = s.d.Store.Settings(context.Background())
	if st.LastError != "" {
		t.Fatalf("LastError = %q", st.LastError)
	}
}

func TestNewestVersionWins(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.5.0"))
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	rel.publish(manifestJSON("1.4.0"))
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := availableVersion(t, s); v != "1.5.0" {
		t.Fatalf("available = %q, want 1.5.0", v)
	}
}

func TestStoredResultIsDroppedOnceRunning(t *testing.T) {
	db := newDB(t)
	pub, priv := newKey(t)
	rel := newRelease(t, priv)
	rel.publish(manifestJSON("1.4.0"))
	deps := Deps{Store: NewStore(db), Version: "1.3.0", Key: pub, ManifestURL: rel.srv.URL + "/m.json"}
	if err := New(deps).CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	deps.Version = "1.4.0" // the panel was updated
	if m, _ := New(deps).Available(context.Background()); m != nil {
		t.Fatalf("offered %q to a panel already running it", m.Version)
	}
}

func TestOversizedManifestIsRefused(t *testing.T) {
	pub, _ := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxFetchBytes+10)))
	}))
	defer srv.Close()
	s := New(Deps{Store: NewStore(newDB(t)), Version: "1.0.0", Key: pub, ManifestURL: srv.URL + "/m.json"})
	err := s.CheckNow(context.Background())
	if err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRefusedWhileLocked(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	s.SetLocked(func() bool { return true })
	if err := s.CheckNow(context.Background()); err != ErrBusy {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if rel.hits.Load() != 0 {
		t.Fatal("nothing should go out while locked")
	}
	st, _ := s.d.Store.Settings(context.Background())
	if st.LastCheckAt != nil {
		t.Fatal("nothing should be written while locked")
	}
}

func TestSchedulerDailyCheck(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	ticks := make(chan time.Time)
	var waits []time.Duration
	s.d.After = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		return ticks
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunScheduler(ctx); close(done) }()

	ticks <- time.Time{} // startup + 5 minutes
	ticks <- time.Time{} // the next day: the first tick has finished once this is taken
	cancel()
	<-done
	if len(waits) < 2 || waits[0] != 5*time.Minute || waits[1] != 24*time.Hour {
		t.Fatalf("waits = %v", waits)
	}
	if got := rel.hits.Load(); got < 2 {
		t.Fatalf("hits = %d, want the first check to have run", got)
	}
}

func TestSchedulerSendsNothingWithCheckOff(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	ctx := context.Background()
	if err := s.d.Store.SetCheckEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	s.tick(ctx)
	if rel.hits.Load() != 0 {
		t.Fatalf("hits = %d with the check off", rel.hits.Load())
	}
	// Check now still works.
	if err := s.CheckNow(ctx); err != nil {
		t.Fatal(err)
	}
	if v := availableVersion(t, s); v != "1.4.0" {
		t.Fatalf("available = %q", v)
	}
	// And a tick with the check on goes out.
	_ = s.d.Store.SetCheckEnabled(ctx, true)
	before := rel.hits.Load()
	s.tick(ctx)
	if rel.hits.Load() == before {
		t.Fatal("a tick with the check on should go out")
	}
}

func TestSchedulerSkipsWhileLocked(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	s.SetLocked(func() bool { return true })
	s.tick(context.Background())
	if rel.hits.Load() != 0 {
		t.Fatal("a tick while locked should go nowhere")
	}
}

func TestBuildWithoutReleaseKeyVerifiesNothing(t *testing.T) {
	saved := releasePublicKey
	releasePublicKey = ""
	t.Cleanup(func() { releasePublicKey = saved })
	_, priv := newKey(t)
	rel := newRelease(t, priv)
	rel.publish(manifestJSON("1.4.0"))
	s := New(Deps{Store: NewStore(newDB(t)), Version: "1.3.0", ManifestURL: rel.srv.URL + "/m.json"})
	if !s.NoKey() {
		t.Fatal("an empty releasePublicKey is no key")
	}
	if err := s.CheckNow(context.Background()); err != ErrNoKey {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
	if rel.hits.Load() != 0 || availableVersion(t, s) != "" {
		t.Fatal("nothing should be fetched or offered")
	}
	_, st := serve(t, s, "GET", "/api/panel-update/status", "")
	if st.Message != ErrNoKey.Error() {
		t.Fatalf("message = %q", st.Message)
	}
}

func TestSmallOrderKeysAreRejected(t *testing.T) {
	zero := make([]byte, 32)
	one := make([]byte, 32)
	one[0] = 1
	minusOne := make([]byte, 32) // y = p-1
	minusOne[0], minusOne[31] = 0xec, 0x7f
	for i := 1; i < 31; i++ {
		minusOne[i] = 0xff
	}
	nonCanonical := append([]byte(nil), minusOne...) // y = p+1 reduces to 1
	nonCanonical[0] = 0xee
	for name, k := range map[string][]byte{"zero": zero, "identity": one, "order-2": minusOne, "non-canonical": nonCanonical} {
		if err := checkKey(k); err == nil {
			t.Errorf("%s key accepted", name)
		}
		if _, err := DecodeKey(base64.StdEncoding.EncodeToString(k)); err == nil {
			t.Errorf("%s key decoded", name)
		}
		// A signature made for such a key (all zero: R = 0, S = 0) verifies nothing.
		if _, err := ParseManifest(manifestJSON("1.4.0"), make([]byte, 64), k); err == nil {
			t.Errorf("%s key verified a crafted signature", name)
		}
	}
	pub, _ := newKey(t)
	if err := checkKey(pub); err != nil {
		t.Fatalf("a real key: %v", err)
	}
}

func TestLockTurningOnDuringFetchWritesNothing(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, rel := newService(t, "1.3.0")
		rel.publish(manifestJSON("1.4.0"))
		rel.fail.Store(fail)
		var on atomic.Bool
		s.SetLocked(on.Load)
		rel.onHit = func() { on.Store(true) } // the update starts while the check is in flight
		if err := s.CheckNow(context.Background()); err != ErrBusy {
			t.Fatalf("fail=%v: err = %v, want ErrBusy", fail, err)
		}
		st, _ := s.d.Store.Settings(context.Background())
		if st.LastCheckAt != nil || st.LastError != "" || availableVersion(t, s) != "" {
			t.Fatalf("fail=%v: wrote %+v", fail, st)
		}
	}
}

func TestVersionParsing(t *testing.T) {
	for in, ok := range map[string]bool{
		"1.2.3": true, "v1.2.3": true, "1.2.3-rc.1": true, "v1.2.3-rc.10": true, "0.0.1-rc.0": true,
		"dev": false, "1.2": false, "1.2.3-rc1": false, "1.2.3-rc.": false, "1.2.3-beta.1": false,
		"1.2.3-rc.1+x": false, "1.2.3-rc.-1": false, "1.2.3\n": false, "": false,
		"0.0.0": true, "10.20.30-rc.100": true,
		"01.2.3": false, "1.02.3": false, "1.2.03": false, "1.2.3-rc.01": false, "1.2.3-rc.00": false,
	} {
		if _, got := parseVersion(in); got != ok {
			t.Errorf("parseVersion(%q) = %v, want %v", in, got, ok)
		}
	}
}

func TestVersionOrder(t *testing.T) {
	chain := []string{"0.1.0-rc.1", "0.1.0-rc.2", "0.1.0-rc.9", "0.1.0-rc.10", "0.1.0", "0.1.1-rc.1", "0.1.1", "1.0.0-rc.1"}
	for i := range chain {
		for j := range chain {
			a, b := mustVersion(chain[i]), mustVersion(chain[j])
			if got, want := a.greater(b), i > j; got != want {
				t.Errorf("%s > %s = %v, want %v", chain[i], chain[j], got, want)
			}
		}
	}
}
