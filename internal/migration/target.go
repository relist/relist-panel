package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Phase string

const (
	PhaseIdle       Phase = ""
	PhaseConnected  Phase = "connected" // claimed, preflight cached
	PhaseLocking    Phase = "locking"   // start sent, waiting for the manifest
	PhasePulling    Phase = "pulling"
	PhasePaused     Phase = "paused"     // an item failed three times
	PhaseFinalizing Phase = "finalizing" // every item passed; the panel restarts
	PhaseDone       Phase = "done"
)

const maxAttempts = 3

// Restorer writes items on the new host.
type Restorer interface {
	Restore(ctx context.Context, item Item, body io.Reader) error // writes the item
	Cleanup(ctx context.Context, item Item) error                 // removes what a failed attempt wrote
	Discard(ctx context.Context, items []Item) error              // abort: removes everything restored
}

// ImageRestorer restores Images, pulling them when it can. fetch opens the
// old host's `docker save` stream, for an Image that cannot be pulled.
type ImageRestorer interface {
	RestoreImage(ctx context.Context, item Item, fetch func(ctx context.Context) (io.ReadCloser, error)) (warning string, err error)
}

// Finalizer moves the staged data into place and restarts the panel (Begin),
// then, after the restart, starts what was running (Complete).
type Finalizer interface {
	Begin(ctx context.Context, m Manifest) error
	Complete(ctx context.Context, m Manifest) (Summary, error)
}

// BlockedError refuses Start while a blocking check stands.
type BlockedError struct{ Message string }

func (e *BlockedError) Error() string { return e.Message }

type ItemProgress struct {
	ID       string   `json:"id"`
	Kind     ItemKind `json:"kind"`
	Size     int64    `json:"size"`
	Done     int64    `json:"done"`
	State    string   `json:"state"` // pending | running | done | failed
	Attempts int      `json:"attempts"`
	Error    string   `json:"error,omitempty"`
}

type Progress struct {
	Phase     Phase          `json:"phase"`
	SourceURL string         `json:"sourceUrl"`
	LockStep  Step           `json:"lockStep"`
	Selected  []string       `json:"selected"`
	Items     []ItemProgress `json:"items"`
	Checks    []Check        `json:"checks"`
	Info      *SourceInfo    `json:"info"`
	Warnings  []string       `json:"warnings"`
	Error     string         `json:"error,omitempty"`
	Summary   *Summary       `json:"summary,omitempty"`
}

type TargetDeps struct {
	Local     LocalHost
	HTTP      *http.Client // nil means a client with no overall timeout
	Insecure  bool         // allow an http:// source, for tests and probes
	StateDir  string       // <data dir>/.migration in production
	Restore   Restorer
	Finalizer Finalizer
	Backoff   func(attempt int) time.Duration // nil means 2s, 8s, 30s
	// Context bounds the background pull; the handler's context in production.
	Context context.Context
}

type itemState struct {
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
}

// targetState is state.json: everything needed to continue after a restart.
type targetState struct {
	Phase      Phase                 `json:"phase"`
	SourceURL  string                `json:"sourceUrl"`
	SelfURL    string                `json:"selfUrl"`
	SessionKey string                `json:"sessionKey"`
	Selected   []string              `json:"selected"`
	Info       *SourceInfo           `json:"info"`
	Checks     []Check               `json:"checks"`
	LockStep   Step                  `json:"lockStep"`
	Manifest   *Manifest             `json:"manifest"`
	Items      map[string]*itemState `json:"items"`
	Warnings   []string              `json:"warnings"`
	Error      string                `json:"error,omitempty"`
}

// Target is the new host's side of a Migration.
type Target struct {
	d TargetDeps

	mu      sync.Mutex
	st      targetState
	done    map[string]*atomic.Int64 // bytes read per item, in memory only
	running bool
	cancel  context.CancelFunc
	// restarted is set when the panel came back up after Begin: what is left
	// is Complete, never Begin again.
	restarted bool
	summary   *Summary
}

func NewTarget(d TargetDeps) *Target {
	if d.HTTP == nil {
		d.HTTP = &http.Client{}
	}
	if d.Backoff == nil {
		d.Backoff = func(attempt int) time.Duration {
			return [...]time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}[min(attempt, maxAttempts)-1]
		}
	}
	if d.Context == nil {
		d.Context = context.Background()
	}
	return &Target{d: d, done: map[string]*atomic.Int64{}}
}

func (t *Target) statePath() string { return filepath.Join(t.d.StateDir, "state.json") }

// save writes state.json atomically. The caller holds t.mu.
func (t *Target) save() error {
	if err := os.MkdirAll(t.d.StateDir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(t.st)
	if err != nil {
		return err
	}
	tmp := t.statePath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.statePath())
}

func (t *Target) client() *client {
	return &client{http: t.d.HTTP, base: strings.TrimRight(t.st.SourceURL, "/"), sessionKey: t.st.SessionKey}
}

// Connect claims the token and runs preflight with every non-system host
// path selected.
func (t *Target) Connect(ctx context.Context, sourceURL, token, selfURL string) (Progress, error) {
	if !strings.HasPrefix(sourceURL, "https://") && !(t.d.Insecure && strings.HasPrefix(sourceURL, "http://")) {
		return Progress{}, errors.New("The source must use https://")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Phase != PhaseIdle && t.st.Phase != PhaseConnected {
		return Progress{}, errors.New("a migration is already running")
	}
	c := &client{http: t.d.HTTP, base: strings.TrimRight(sourceURL, "/")}
	key, err := c.claim(ctx, token, selfURL)
	if err != nil {
		return Progress{}, err
	}
	c.sessionKey = key
	info, err := c.preflight(ctx)
	if err != nil {
		return Progress{}, err
	}
	selected := []string{}
	for _, p := range info.HostPaths {
		if !p.System {
			selected = append(selected, p.Path)
		}
	}
	checks, err := Evaluate(ctx, t.d.Local, info, selected)
	if err != nil {
		return Progress{}, err
	}
	t.st = targetState{Phase: PhaseConnected, SourceURL: c.base, SelfURL: selfURL, SessionKey: key,
		Selected: selected, Info: &info, Checks: checks, Warnings: []string{}}
	if err := t.save(); err != nil {
		return Progress{}, err
	}
	return t.progress(), nil
}

// Recheck re-runs preflight for another host path selection.
func (t *Target) Recheck(ctx context.Context, selected []string) (Progress, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Phase != PhaseConnected {
		return Progress{}, errors.New("connect to a source first")
	}
	if err := t.recheck(ctx, selected); err != nil {
		return Progress{}, err
	}
	return t.progress(), nil
}

func (t *Target) recheck(ctx context.Context, selected []string) error {
	if selected == nil {
		selected = []string{}
	}
	info, err := t.client().preflight(ctx)
	if err != nil {
		return err
	}
	checks, err := Evaluate(ctx, t.d.Local, info, selected)
	if err != nil {
		return err
	}
	t.st.Info, t.st.Checks, t.st.Selected = &info, checks, selected
	return t.save()
}

// Start locks the source and pulls every item in the background.
func (t *Target) Start(ctx context.Context, selected []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Phase != PhaseConnected {
		return errors.New("connect to a source first")
	}
	if err := t.recheck(ctx, selected); err != nil {
		return err
	}
	for _, c := range t.st.Checks {
		if c.Blocking {
			return &BlockedError{Message: c.Message}
		}
	}
	t.st.Phase = PhaseLocking
	if err := t.save(); err != nil {
		return err
	}
	t.launch()
	return nil
}

// launch starts the background loop. The caller holds t.mu.
func (t *Target) launch() {
	if t.running {
		return
	}
	ctx, cancel := context.WithCancel(t.d.Context)
	t.running, t.cancel = true, cancel
	go func() {
		defer cancel()
		err := t.run(ctx)
		t.mu.Lock()
		t.running = false
		if err != nil && ctx.Err() == nil {
			log.Printf("migration: %v", err)
			t.st.Error = err.Error()
			t.save()
		}
		t.mu.Unlock()
	}()
}

// run takes the Migration from locking through finalizing.
func (t *Target) run(ctx context.Context) error {
	t.mu.Lock()
	phase := t.st.Phase
	t.mu.Unlock()
	if phase == PhaseLocking {
		if err := t.waitManifest(ctx); err != nil {
			return err
		}
	}
	t.mu.Lock()
	m := *t.st.Manifest
	t.mu.Unlock()
	for _, item := range m.Items {
		t.mu.Lock()
		state := t.st.Items[item.ID].State
		t.mu.Unlock()
		if state == "done" {
			continue
		}
		if !t.pull(ctx, item) {
			return nil
		}
	}
	t.mu.Lock()
	t.st.Phase = PhaseFinalizing
	err := t.save()
	t.mu.Unlock()
	if err != nil {
		return err
	}
	return t.d.Finalizer.Begin(ctx, m)
}

func (t *Target) waitManifest(ctx context.Context) error {
	for {
		t.mu.Lock()
		c, selected := t.client(), t.st.Selected
		t.mu.Unlock()
		m, step, lastErr, err := c.start(ctx, selected)
		if err != nil {
			return fmt.Errorf("lock the source: %w", err)
		}
		t.mu.Lock()
		t.st.LockStep, t.st.Error = step, lastErr
		if m != nil {
			t.st.Manifest, t.st.Phase = m, PhasePulling
			t.st.Items = map[string]*itemState{}
			for _, it := range m.Items {
				t.st.Items[it.ID] = &itemState{State: "pending"}
			}
			t.st.Warnings = append(t.st.Warnings, m.Warnings...)
		}
		err = t.save()
		t.mu.Unlock()
		if err != nil || m != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// pull restores one item with retries. It reports false when the Migration
// paused or ctx ended.
func (t *Target) pull(ctx context.Context, item Item) bool {
	for {
		t.mu.Lock()
		is := t.st.Items[item.ID]
		is.State, is.Error = "running", ""
		counter := &atomic.Int64{}
		t.done[item.ID] = counter
		t.save()
		t.mu.Unlock()

		if err := t.d.Restore.Cleanup(ctx, item); err != nil {
			log.Printf("migration: clean up %s: %v", item.ID, err)
		}
		warning, err := t.attempt(ctx, item, counter)
		if ctx.Err() != nil {
			return false
		}
		t.mu.Lock()
		if err == nil {
			is.State = "done"
			if warning != "" {
				t.st.Warnings = append(t.st.Warnings, warning)
			}
			t.save()
			t.mu.Unlock()
			return true
		}
		is.Attempts++
		is.Error = err.Error()
		if is.Attempts >= maxAttempts {
			is.State = "failed"
			t.st.Phase = PhasePaused
			t.save()
			t.mu.Unlock()
			t.d.Restore.Cleanup(ctx, item)
			return false
		}
		is.State = "pending"
		attempts := is.Attempts
		t.save()
		t.mu.Unlock()
		t.d.Restore.Cleanup(ctx, item)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(t.d.Backoff(attempts)):
		}
	}
}

// countingReader counts and hashes what it reads.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
	h io.Writer
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	c.h.Write(p[:n])
	return n, err
}

// stream opens an item, hashing it as it is read; check compares the hash
// with the old host's digest once the body is consumed.
func (t *Target) stream(ctx context.Context, id string, counter *atomic.Int64) (io.ReadCloser, func() error, error) {
	t.mu.Lock()
	c := t.client()
	t.mu.Unlock()
	body, err := c.item(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	h := sha256.New()
	reader := struct {
		io.Reader
		io.Closer
	}{countingReader{r: body, n: counter, h: h}, body}
	check := func() error {
		want, err := c.digest(ctx, id)
		if err != nil {
			return fmt.Errorf("digest of %s: %w", id, err)
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			return fmt.Errorf("%s arrived with SHA-256 %s, but the source sent %s", id, got, want)
		}
		return nil
	}
	return reader, check, nil
}

func (t *Target) attempt(ctx context.Context, item Item, counter *atomic.Int64) (string, error) {
	if images, ok := t.d.Restore.(ImageRestorer); ok && item.Kind == KindImage {
		var checks []func() error
		fetch := func(ctx context.Context) (io.ReadCloser, error) {
			body, check, err := t.stream(ctx, item.ID, counter)
			if err == nil {
				checks = append(checks, check)
			}
			return body, err
		}
		warning, err := images.RestoreImage(ctx, item, fetch)
		if err != nil {
			return "", err
		}
		for _, check := range checks {
			if err := check(); err != nil {
				return "", err
			}
		}
		return warning, nil
	}
	body, check, err := t.stream(ctx, item.ID, counter)
	if err != nil {
		return "", err
	}
	defer body.Close()
	if err := t.d.Restore.Restore(ctx, item, body); err != nil {
		return "", err
	}
	// Drain anything the restorer left, so the hash covers the whole body.
	if _, err := io.Copy(io.Discard, body); err != nil {
		return "", err
	}
	return "", check()
}

// Retry continues a paused Migration, giving failed items three more tries.
func (t *Target) Retry(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	stalled := !t.running && (t.st.Phase == PhaseLocking || t.st.Phase == PhasePulling || t.st.Phase == PhaseFinalizing)
	if t.st.Phase != PhasePaused && !stalled {
		return errors.New("nothing to retry")
	}
	if t.restarted {
		t.st.Error = ""
		t.save()
		t.completeAsync()
		return nil
	}
	for _, is := range t.st.Items {
		if is.State == "failed" {
			is.State, is.Attempts = "pending", 0
		}
	}
	if t.st.Manifest != nil && t.st.Phase != PhaseFinalizing {
		t.st.Phase = PhasePulling
	}
	t.st.Error = ""
	if err := t.save(); err != nil {
		return err
	}
	t.launch()
	return nil
}

// Abort tells the old host to resume, removes everything restored and
// forgets the Migration.
func (t *Target) Abort(ctx context.Context) error {
	t.mu.Lock()
	if t.st.Phase == PhaseFinalizing || t.st.Phase == PhaseDone {
		t.mu.Unlock()
		return errors.New("the migration is finalizing and can no longer be aborted here; resume the old server from its own panel instead")
	}
	if t.cancel != nil {
		t.cancel()
	}
	c, m, claimed := t.client(), t.st.Manifest, t.st.SessionKey != ""
	var restored []Item
	if m != nil {
		for _, it := range m.Items {
			if is := t.st.Items[it.ID]; is != nil && is.State != "pending" {
				restored = append(restored, it)
			}
		}
	}
	t.mu.Unlock()

	if claimed {
		if err := c.abort(ctx); err != nil {
			log.Printf("migration: tell the source to abort: %v", err)
		}
	}
	if len(restored) > 0 {
		if err := t.d.Restore.Discard(ctx, restored); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(t.d.StateDir); err != nil {
		return err
	}
	t.mu.Lock()
	t.st = targetState{}
	t.done = map[string]*atomic.Int64{}
	t.mu.Unlock()
	return nil
}

// Progress is what the setup screen polls.
func (t *Target) Progress() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.progress()
}

func (t *Target) progress() Progress {
	p := Progress{Phase: t.st.Phase, SourceURL: t.st.SourceURL, LockStep: t.st.LockStep, Selected: t.st.Selected,
		Items: []ItemProgress{}, Checks: t.st.Checks, Info: t.st.Info, Warnings: t.st.Warnings, Error: t.st.Error, Summary: t.summary}
	if p.Checks == nil {
		p.Checks = []Check{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	if p.Selected == nil {
		p.Selected = []string{}
	}
	if t.st.Manifest != nil {
		for _, it := range t.st.Manifest.Items {
			ip := ItemProgress{ID: it.ID, Kind: it.Kind, Size: it.Size, State: "pending"}
			if is := t.st.Items[it.ID]; is != nil {
				ip.State, ip.Attempts, ip.Error = is.State, is.Attempts, is.Error
			}
			if c := t.done[it.ID]; c != nil {
				ip.Done = c.Load()
			}
			if ip.State == "done" {
				ip.Done = max(ip.Done, it.Size)
			}
			p.Items = append(p.Items, ip)
		}
	}
	return p
}

// ResumeIfPending loads state.json at boot and carries on: an interrupted
// pull continues, and a finalizing Migration completes.
func (t *Target) ResumeIfPending(ctx context.Context) error {
	raw, err := os.ReadFile(t.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	t.mu.Lock()
	if err := json.Unmarshal(raw, &t.st); err != nil {
		t.mu.Unlock()
		return fmt.Errorf("read %s: %w", t.statePath(), err)
	}
	phase := t.st.Phase
	switch phase {
	case PhaseLocking, PhasePulling:
		for _, is := range t.st.Items {
			if is.State == "running" {
				is.State = "pending"
			}
		}
		t.launch()
	}
	if phase == PhaseFinalizing {
		t.restarted = true
		t.completeAsync()
	}
	t.mu.Unlock()
	return nil
}

// completeAsync runs complete in the background. The caller holds t.mu.
func (t *Target) completeAsync() {
	if t.running {
		return
	}
	ctx, cancel := context.WithCancel(t.d.Context)
	t.running, t.cancel = true, cancel
	go func() {
		defer cancel()
		err := t.complete(ctx)
		t.mu.Lock()
		t.running = false
		if err != nil && ctx.Err() == nil {
			log.Printf("migration: complete: %v", err)
			t.st.Error = err.Error()
			t.save()
		}
		t.mu.Unlock()
	}()
}

// complete runs after the restart: Traefik and the Deployments, then finish
// on the old host, then summary.json in place of the state.
func (t *Target) complete(ctx context.Context) error {
	t.mu.Lock()
	var m Manifest
	if t.st.Manifest != nil {
		m = *t.st.Manifest
	}
	c, self, warnings := t.client(), t.st.SelfURL, slices.Clone(t.st.Warnings)
	t.mu.Unlock()

	summary, err := t.d.Finalizer.Complete(ctx, m)
	if err != nil {
		return err
	}
	var finishErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if finishErr = c.finish(ctx, self); finishErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.d.Backoff(attempt)):
		}
	}
	summary.Warnings = append(warnings, summary.Warnings...)
	if finishErr != nil {
		summary.Warnings = append(summary.Warnings, "Could not tell the old server the migration finished ("+finishErr.Error()+"); it stays locked, so turn it off or resume it from its own panel")
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(t.d.StateDir, "summary.json"), raw, 0o600); err != nil {
		return err
	}
	for _, name := range []string{"state.json", markerName, "staging"} {
		if err := os.RemoveAll(filepath.Join(t.d.StateDir, name)); err != nil {
			return err
		}
	}
	t.mu.Lock()
	t.st.Phase, t.summary = PhaseDone, &summary
	t.mu.Unlock()
	return nil
}
