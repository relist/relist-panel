package registry

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrCleanupBusy is a second cleanup while one runs.
var ErrCleanupBusy = errors.New("a Registry cleanup is already running")

// emptyRegistryGC is what garbage-collect prints, exiting 1, on a registry
// nothing was ever pushed to (probe 2026-11-06): nothing to collect.
const emptyRegistryGC = "Path not found: /docker/registry/v2/repositories"

// Gate says when cleanup must wait: a Deployment runs (Busy), or a Panel
// update or Migration holds the panel (Locked).
type Gate interface {
	Busy() bool
	Locked() bool
}

// RegistryAPI is the part of the registry API cleanup uses; *Client is one.
type RegistryAPI interface {
	Repositories(ctx context.Context) ([]string, error)
	Tags(ctx context.Context, repo string) ([]string, error)
	TagInfo(ctx context.Context, repo, tag string) (TagInfo, error)
	Delete(ctx context.Context, repo, digest string) error
}

type CleanerDeps struct {
	Service *Service
	Gate    Gate
	// InUse is every image reference a Compose runs, holds or last deployed,
	// as host/repo:tag and host/repo@digest.
	InUse func(ctx context.Context) (map[string]bool, error)
	// Registry reaches the registry API with the system login.
	Registry func(Settings) RegistryAPI
	Poll     time.Duration // how often a waiting run checks the Gate
	GCPoll   time.Duration // how often a run checks the GC container
	Now      func() time.Time
}

// Cleaner runs Registry cleanup: retention, then garbage collection with the
// registry read-only (ADR-0033). One run at a time.
type Cleaner struct {
	d    CleanerDeps
	mu   sync.Mutex
	done chan struct{} // closed when the current run ends; nil when idle
}

// NewCleaner fills in the defaults: a Client on InternalBase, 10 s and 2 s
// polls, time.Now.
func NewCleaner(d CleanerDeps) *Cleaner {
	if d.Registry == nil {
		d.Registry = func(st Settings) RegistryAPI {
			return &Client{Base: InternalBase, User: SystemUser, Secret: st.SystemSecret, HTTP: http.DefaultClient}
		}
	}
	if d.Poll == 0 {
		d.Poll = 10 * time.Second
	}
	if d.GCPoll == 0 {
		d.GCPoll = 2 * time.Second
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Cleaner{d: d}
}

func (c *Cleaner) store() *Store { return c.d.Service.d.Store }
func (c *Cleaner) host() Host    { return c.d.Service.d.Host }

// Running reports whether a run is in progress.
func (c *Cleaner) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done != nil
}

// RunNow records a run and starts it in the background; it waits for the
// Gate before touching the registry. ctx's values are kept, not its
// cancellation, so the run outlives the request.
func (c *Cleaner) RunNow(ctx context.Context, trigger string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done != nil {
		return 0, ErrCleanupBusy
	}
	id, err := c.store().StartRun(ctx, c.d.Now(), trigger)
	if err != nil {
		return 0, err
	}
	done := make(chan struct{})
	c.done = done
	go func() {
		defer func() {
			c.mu.Lock()
			c.done = nil
			c.mu.Unlock()
			close(done)
		}()
		c.run(context.WithoutCancel(ctx), id)
	}()
	return id, nil
}

// WaitIdle returns once no run is in progress, or ctx ends.
func (c *Cleaner) WaitIdle(ctx context.Context) error {
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Cleaner) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (c *Cleaner) run(ctx context.Context, id int64) {
	var logs strings.Builder
	logf := func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }
	finish := func(result string, deleted []string, freed int64) {
		if err := c.store().FinishRun(ctx, id, c.d.Now(), result, deleted, freed, logs.String()); err != nil {
			log.Printf("registry cleanup: record run %d: %v", id, err)
		}
	}
	for c.d.Gate != nil && (c.d.Gate.Busy() || c.d.Gate.Locked()) {
		if err := c.sleep(ctx, c.d.Poll); err != nil {
			logf("stopped while waiting: %v", err)
			finish(RunFailed, nil, 0)
			return
		}
	}
	st, err := c.store().Settings(ctx)
	if err != nil || !st.Enabled {
		logf("the Registry is off")
		finish(RunFailed, nil, 0)
		return
	}
	settings, err := c.store().CleanupSettings(ctx)
	if err != nil {
		logf("read cleanup settings: %v", err)
		finish(RunFailed, nil, 0)
		return
	}
	before, _ := c.host().VolumeBytes(ctx)
	deleted, err := c.retain(ctx, st, settings.Keep, logf)
	if err != nil {
		logf("retention stopped: %v; garbage collection skipped this run", err)
		finish(RunFailed, deleted, 0)
		return
	}
	result := RunOK
	if err := c.collect(ctx, id, logf); err != nil {
		logf("garbage collection failed: %v", err)
		result = RunFailed
	}
	after, _ := c.host().VolumeBytes(ctx)
	finish(result, deleted, max(before-after, 0))
}

// retain deletes what SelectDeletions picks in each repository. Before each
// delete it reads the repository's tags again and skips a digest that a tag
// outside the selection now points at (a push raced). It returns repo:tag
// for every tag deleted.
func (c *Cleaner) retain(ctx context.Context, st Settings, keep int, logf func(string, ...any)) ([]string, error) {
	inUse := map[string]bool{}
	if c.d.InUse != nil {
		var err error
		if inUse, err = c.d.InUse(ctx); err != nil {
			return nil, fmt.Errorf("read images in use: %w", err)
		}
	}
	reg := c.d.Registry(st)
	repos, err := reg.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, repo := range repos {
		infos, err := tagInfos(ctx, reg, repo)
		if err != nil {
			return deleted, err
		}
		digests, tags := SelectDeletions(infos, repoInUse(inUse, st.Hostname, repo, infos), keep)
		if len(digests) == 0 {
			continue
		}
		selected := map[string]bool{}
		for _, t := range tags {
			selected[t] = true
		}
		for _, digest := range digests {
			now, err := tagInfos(ctx, reg, repo)
			if err != nil {
				return deleted, err
			}
			var gone, raced []string
			for _, info := range now {
				if info.Digest != digest {
					continue
				}
				if selected[info.Tag] {
					gone = append(gone, info.Tag)
				} else {
					raced = append(raced, info.Tag)
				}
			}
			if len(raced) > 0 {
				logf("%s: kept %s, now also tagged %s", repo, digest, strings.Join(raced, ", "))
				continue
			}
			if err := reg.Delete(ctx, repo, digest); err != nil {
				return deleted, fmt.Errorf("delete %s@%s: %w", repo, digest, err)
			}
			slices.Sort(gone)
			for _, t := range gone {
				deleted = append(deleted, repo+":"+t)
			}
			logf("%s: deleted %s (%s)", repo, digest, strings.Join(gone, ", "))
		}
	}
	return deleted, nil
}

func tagInfos(ctx context.Context, reg RegistryAPI, repo string) ([]TagInfo, error) {
	tags, err := reg.Tags(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]TagInfo, 0, len(tags))
	for _, tag := range tags {
		info, err := reg.TagInfo(ctx, repo, tag)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// collect runs garbage-collect with the registry read-only, and always puts
// it back read-write.
func (c *Cleaner) collect(ctx context.Context, id int64, logf func(string, ...any)) (err error) {
	if err := c.store().SetGCRunning(ctx, id); err != nil {
		return err
	}
	defer func() {
		if rerr := c.restoreWritable(ctx); rerr != nil && err == nil {
			err = rerr
		}
	}()
	if err := c.setReadOnly(ctx, true); err != nil {
		return err
	}
	_ = c.host().RemoveGC(ctx) // a leftover from an earlier run
	if err := c.host().RunGC(ctx, c.d.Service.d.Image, c.d.Service.d.Files.Dir); err != nil {
		return err
	}
	state, err := c.waitGC(ctx)
	if err != nil {
		return err
	}
	logf("%s", strings.TrimSpace(state.Logs))
	if state.ExitCode != 0 && !strings.Contains(state.Logs, emptyRegistryGC) {
		return fmt.Errorf("garbage-collect exited %d", state.ExitCode)
	}
	return nil
}

func (c *Cleaner) waitGC(ctx context.Context) (GCState, error) {
	for {
		state, err := c.host().GCState(ctx)
		if err != nil || !state.Running {
			return state, err
		}
		if err := c.sleep(ctx, c.d.GCPoll); err != nil {
			return state, err
		}
	}
}

// setReadOnly rewrites config.yml with the maintenance flag and restarts the
// registry to read it.
func (c *Cleaner) setReadOnly(ctx context.Context, readOnly bool) error {
	svc := c.d.Service
	svc.mu.Lock()
	defer svc.mu.Unlock()
	st, err := svc.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	creds, err := svc.d.Store.Credentials(ctx)
	if err != nil {
		return err
	}
	if err := svc.d.Files.Write(RenderConfig(readOnly), RenderHtpasswd(st.SystemHash, creds)); err != nil {
		return err
	}
	return svc.d.Host.Restart(ctx)
}

// restoreWritable is the part that always runs: read-write, restart, remove
// the GC container, clear gc_running.
func (c *Cleaner) restoreWritable(ctx context.Context) error {
	err := c.setReadOnly(ctx, false)
	if rerr := c.host().RemoveGC(ctx); err == nil {
		err = rerr
	}
	if serr := c.store().SetGCRunning(ctx, 0); err == nil {
		err = serr
	}
	return err
}

// Startup finishes a run the panel was killed during: it waits for the GC
// container, puts the registry back read-write and records the run as
// interrupted.
func (c *Cleaner) Startup(ctx context.Context) error {
	id, err := c.store().GCRunning(ctx)
	if err != nil || id == 0 {
		return err
	}
	state, err := c.waitGC(ctx)
	if err != nil {
		return err
	}
	rerr := c.restoreWritable(ctx)
	msg := "the panel stopped during garbage collection\n" + strings.TrimSpace(state.Logs)
	if ferr := c.store().FinishRun(ctx, id, c.d.Now(), RunInterrupted, nil, 0, msg); rerr == nil {
		rerr = ferr
	}
	return rerr
}

// RunScheduler checks each minute whether a scheduled run is due, until ctx
// ends.
func (c *Cleaner) RunScheduler(ctx context.Context) {
	for {
		c.tick(ctx)
		if c.sleep(ctx, time.Minute) != nil {
			return
		}
	}
}

func (c *Cleaner) tick(ctx context.Context) {
	st, err := c.store().Settings(ctx)
	if err != nil || !st.Enabled {
		return
	}
	settings, err := c.store().CleanupSettings(ctx)
	if err != nil {
		log.Printf("registry cleanup: read settings: %v", err)
		return
	}
	now := c.d.Now()
	if !Due(now, settings) || c.Running() {
		return
	}
	settings.LastRunAt = &now
	if err := c.store().SaveCleanupSettings(ctx, settings); err != nil {
		log.Printf("registry cleanup: record schedule: %v", err)
		return
	}
	if _, err := c.RunNow(ctx, "schedule"); err != nil && !errors.Is(err, ErrCleanupBusy) {
		log.Printf("registry cleanup: %v", err)
	}
}

// preview is what a run would delete now: repo → tags, only repositories
// with something to delete.
func (c *Cleaner) preview(ctx context.Context, inUse func(context.Context) (map[string]bool, error)) (map[string][]string, error) {
	st, err := c.store().Settings(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := c.store().CleanupSettings(ctx)
	if err != nil {
		return nil, err
	}
	used, err := inUse(ctx)
	if err != nil {
		return nil, err
	}
	reg := c.d.Registry(st)
	repos, err := reg.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, repo := range repos {
		infos, err := tagInfos(ctx, reg, repo)
		if err != nil {
			return nil, err
		}
		if _, tags := SelectDeletions(infos, repoInUse(used, st.Hostname, repo, infos), settings.Keep); len(tags) > 0 {
			out[repo] = tags
		}
	}
	return out, nil
}

// repoInUse marks the tags of repo that inUse names by tag or by digest.
func repoInUse(inUse map[string]bool, hostname, repo string, infos []TagInfo) map[string]bool {
	used := map[string]bool{}
	base := hostname + "/" + repo
	for _, info := range infos {
		if inUse[base+":"+info.Tag] || inUse[base+"@"+info.Digest] {
			used[info.Tag] = true
		}
	}
	return used
}
