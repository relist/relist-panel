package panelupdate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultManifestURL is the latest release's Update manifest. The
	// signature is the same URL plus ".sig".
	DefaultManifestURL = "https://github.com/relist/relist-panel/releases/latest/download/update-manifest.json"

	checkTimeout  = 30 * time.Second
	maxFetchBytes = 10 << 20
	firstCheck    = 5 * time.Minute
	checkEvery    = 24 * time.Hour
)

// ManifestURLFromEnv is the manifest location: RELIST_UPDATE_MANIFEST_URL, or
// the default. The signature is checked either way, so an override is safe.
func ManifestURLFromEnv() string {
	if v := os.Getenv("RELIST_UPDATE_MANIFEST_URL"); v != "" {
		return v
	}
	return DefaultManifestURL
}

// ErrBusy means a check was refused because an update is running.
var ErrBusy = errors.New("a Panel update is running")

type Deps struct {
	Store *Store
	// Version is the running build's version, "dev" when not stamped.
	Version string
	// Key verifies manifests. Empty means the committed release key.
	Key ed25519.PublicKey
	// ManifestURL is where the manifest is fetched. Empty means the default.
	ManifestURL string
	// Client fetches the manifest. Nil means a client with checkTimeout.
	Client *http.Client
	Now    func() time.Time
	// Self and Helper find and read the Install. Nil makes it unavailable.
	Self   SelfInspector
	Helper Helper
	// Docker pulls the new image and lists the running Containers' config
	// hashes for the Preflight. Nil makes the Preflight fail at that step.
	Docker Docker
	// DataDir holds the Preflight's scratch directory.
	DataDir string
	// InstallFile is the Install file this build embeds: the base of the
	// change check and the merge.
	InstallFile []byte
	// MigrationLocked and DeploysBusy are the Preflight's Gate. Nil means
	// not locked and not busy.
	MigrationLocked func(ctx context.Context) (bool, error)
	DeploysBusy     func() bool
	// Queue, Scans and Updater are what the update lock drives and watches.
	// Nil leaves that part out.
	Queue   QueueControl
	Scans   ScanWaiter
	Updater UpdaterContainers
	// Domain is RELIST_DOMAIN: the Host and SNI of the Updater's health request.
	Domain string
	// After returns a channel that fires after d. Nil means time.After.
	After func(d time.Duration) <-chan time.Time
}

type Service struct {
	d  Deps
	mu sync.Mutex // one check at a time
	// locked reports whether the update lock is on. While it is, the check
	// writes nothing. New wires it to Locked.
	locked atomic.Pointer[func() bool]
	// lockOn is the update lock; lockMu orders turning it on and off with the
	// queue's hold. runMu serializes QuiesceAndBackup.
	lockOn atomic.Bool
	lockMu sync.Mutex
	runMu  sync.Mutex
	// guard is the run between QuiesceAndBackup and MarkStarted, which has no
	// Updater Container yet; settled is the last run and state the watcher
	// finished with. Both are guarded by guardMu.
	guardMu sync.Mutex
	guard   string
	settled string
	wake    chan struct{}
	// pfRun serializes Preflights; pfMu guards latest.
	pfRun  sync.Mutex
	pfMu   sync.Mutex
	latest *Report
	// latestFP is the fingerprint of latest.
	latestFP string
}

func New(d Deps) *Service {
	if d.Key == nil && releasePublicKey != "" {
		key, err := DecodeKey(releasePublicKey)
		if err != nil {
			log.Printf("panelupdate: release public key: %v", err)
		}
		d.Key = key
	}
	if d.ManifestURL == "" {
		d.ManifestURL = DefaultManifestURL
	}
	if d.Client == nil {
		d.Client = &http.Client{Timeout: checkTimeout}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.After == nil {
		d.After = time.After
	}
	s := &Service{d: d, wake: make(chan struct{}, 1)}
	s.SetLocked(s.Locked)
	return s
}

// SetLocked installs the update-lock hook. It is safe to call while the
// scheduler runs.
func (s *Service) SetLocked(f func() bool) { s.locked.Store(&f) }

func (s *Service) isLocked() bool {
	f := s.locked.Load()
	return f != nil && (*f)()
}

// ErrNoKey means this build has no usable release key.
var ErrNoKey = errors.New("This build has no release key; updates are not offered")

// NoKey reports a build that cannot verify any manifest.
func (s *Service) NoKey() bool { return checkKey(s.d.Key) != nil }

// Development reports a build that was not stamped with a release version.
// Such a build is never offered an update.
func (s *Service) Development() bool {
	_, ok := parseVersion(s.d.Version)
	return !ok
}

// Available is the newest verified manifest greater than the running version,
// nil when there is none. It is verified again on every read.
func (s *Service) Available(ctx context.Context) (*Manifest, error) {
	running, ok := parseVersion(s.d.Version)
	if !ok {
		return nil, nil
	}
	data, sig, err := s.d.Store.Available(ctx)
	if err != nil || data == nil {
		return nil, err
	}
	m, err := ParseManifest(data, sig, s.d.Key)
	if err != nil {
		log.Printf("panelupdate: drop the stored manifest: %v", err)
		return nil, nil
	}
	if v, _ := parseVersion(m.Version); !v.greater(running) {
		return nil, nil
	}
	return m, nil
}

// CheckNow fetches and verifies the latest manifest and keeps the newest
// version greater than the running one. A failed fetch or verification is
// recorded and keeps the last good result; the returned error is the same text.
func (s *Service) CheckNow(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isLocked() {
		return ErrBusy
	}
	if s.NoKey() {
		return ErrNoKey
	}
	now := s.d.Now()
	cand, sig, err := s.fetch(ctx)
	if err != nil {
		log.Printf("panelupdate: check: %v", err)
		if s.isLocked() { // the lock came on during the fetch
			return ErrBusy
		}
		if werr := s.d.Store.RecordFailure(ctx, now, err.Error()); werr != nil {
			return werr
		}
		return err
	}
	keep, keepSig, err := s.best(ctx, cand, sig)
	if err != nil {
		return err
	}
	if s.isLocked() { // the lock came on during the fetch: write nothing
		return ErrBusy
	}
	return s.d.Store.RecordSuccess(ctx, now, keep, keepSig)
}

// best picks what to store: the stored result or the candidate, whichever is
// newer and greater than the running version.
func (s *Service) best(ctx context.Context, cand *Manifest, sig []byte) ([]byte, []byte, error) {
	running, ok := parseVersion(s.d.Version)
	if !ok {
		return nil, nil, nil
	}
	var bestM *Manifest
	var bestSig []byte
	if cv, _ := parseVersion(cand.Version); cv.greater(running) {
		bestM, bestSig = cand, sig
	}
	stored, err := s.Available(ctx)
	if err != nil {
		return nil, nil, err
	}
	if stored != nil {
		sv, _ := parseVersion(stored.Version)
		if bestM == nil || sv.greater(mustVersion(bestM.Version)) {
			_, storedSig, err := s.d.Store.Available(ctx)
			if err != nil {
				return nil, nil, err
			}
			bestM, bestSig = stored, storedSig
		}
	}
	if bestM == nil {
		return nil, nil, nil
	}
	return bestM.Raw, bestSig, nil
}

func mustVersion(s string) semver {
	v, _ := parseVersion(s)
	return v
}

func (s *Service) fetch(ctx context.Context) (*Manifest, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	data, err := s.get(ctx, s.d.ManifestURL)
	if err != nil {
		return nil, nil, err
	}
	sig, err := s.get(ctx, s.d.ManifestURL+".sig")
	if err != nil {
		return nil, nil, err
	}
	m, err := ParseManifest(data, sig, s.d.Key)
	if err != nil {
		return nil, nil, err
	}
	return m, sig, nil
}

func (s *Service) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the release server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the release server answered %s for %s", resp.Status, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(data) > maxFetchBytes {
		return nil, fmt.Errorf("%s is larger than %d MiB", url, maxFetchBytes>>20)
	}
	return data, nil
}

// RunScheduler checks once at startup plus 5 minutes and then once a day,
// until ctx is done. With the check off, or the update lock on, a tick does
// nothing.
func (s *Service) RunScheduler(ctx context.Context) {
	wait := firstCheck
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.d.After(wait):
		}
		wait = checkEvery
		s.tick(ctx)
	}
}

func (s *Service) tick(ctx context.Context) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		log.Printf("panelupdate: read settings: %v", err)
		return
	}
	if !st.CheckEnabled || s.isLocked() {
		return
	}
	// A failure is already logged and recorded.
	_ = s.CheckNow(ctx)
}
