package deployments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

// Gate serialises work on a Project; the Deploy step holds it.
type Gate interface {
	Acquire(ctx context.Context, projectID int64) (release func(), err error)
}

type Composes interface {
	Target(ctx context.Context, composeID int64) (composes.DeployTarget, error)
	Capture(ctx context.Context, t composes.DeployTarget) (composes.DeployTarget, error)
	CheckDeployable(ctx context.Context, t composes.DeployTarget) error
	PrepareBuild(ctx context.Context, t composes.DeployTarget) (composes.BuildPlan, error)
	RunDeploy(ctx context.Context, t composes.DeployTarget, opts composes.DeployOptions) ([]string, error)
}

type Fetcher interface {
	ResolveRevision(ctx context.Context, src sources.Source, composeDir string) (string, error)
	Fetch(ctx context.Context, src sources.Source, composeDir string, out io.Writer) (sources.Ref, error)
}

type Builder interface {
	Build(ctx context.Context, req composes.RunRequest, opts composes.BuildOptions) error
}

type MaintenanceGate interface {
	Acquire(context.Context) (func(), error)
}

type Deps struct {
	Maintenance MaintenanceGate
	Store       *Store
	Sources     *sources.Store
	Composes    Composes
	Fetcher     Fetcher
	Builder     Builder
	Gate        Gate
	Timeout     time.Duration // 60 * time.Minute in production
	Keep        int           // 20
	Now         func() time.Time
}

type Request struct {
	Mode                  Mode
	NoCache, FreshVolumes bool
}

// ErrNotCancelable is returned for a Deployment that is in its Deploy step or
// already settled.
var ErrNotCancelable = errors.New("deployment can no longer be canceled")

// QueuedConflictError refuses a request whose options differ from the
// Compose's queued Deployment, which would otherwise be reused as it is.
type QueuedConflictError struct{ Queued Deployment }

func (e *QueuedConflictError) Error() string {
	return fmt.Sprintf("deployment #%d is already queued with mode=%s, noCache=%t, freshVolumes=%t; cancel it first",
		e.Queued.Number, e.Queued.Mode, e.Queued.NoCache, e.Queued.FreshVolumes)
}

// sameRequest reports whether r asks for what the queued Deployment d will do.
func sameRequest(d Deployment, r Request) bool {
	return d.Mode == r.Mode && d.NoCache == r.NoCache && d.FreshVolumes == r.FreshVolumes
}

// reuse returns the Compose's queued Deployment for r: it as it is when r asks
// for the same, a QueuedConflictError when it asks for something else.
func reuse(queued Deployment, r Request) (Deployment, bool, error) {
	if !sameRequest(queued, r) {
		return Deployment{}, false, &QueuedConflictError{Queued: queued}
	}
	return queued, false, nil
}

// ErrHeld refuses a Deployment while a server Migration holds the queue.
var ErrHeld = errors.New("deployments are paused while this server is being migrated")

const (
	reasonRestarted      = "panel restarted"
	reasonTimeout        = "timeout"
	reasonComposeDeleted = "compose deleted"
	reasonNoSource       = "compose has no source"
	reasonNoBranch       = "no branch configured"
	reasonInternal       = "internal error"

	stepFetch  = "fetch"
	stepDeploy = "deploy"

	retryDelay = time.Second
)

type phase string

const (
	phaseStarting    phase = "starting"
	phaseFetching    phase = "fetching"
	phaseBuilding    phase = "building"
	phaseWaitingGate phase = "waiting-gate"
	phaseDeploying   phase = "deploying"
)

// activeRun is the in-memory state of the Deployment the worker is on. All fields
// but done are guarded by Queue.mu.
type activeRun struct {
	number   int
	phase    phase
	cancel   context.CancelFunc
	canceled bool
	done     chan struct{} // closed once the Deployment is settled
}

// Queue runs Deployments one at a time, oldest first, across all Composes.
type Queue struct {
	d    Deps
	wake chan struct{}

	mu       sync.Mutex
	running  map[int64]*activeRun
	srcLocks map[int64]chan struct{}

	// holdMu is held around the insert, so once Hold returns no Enqueue
	// can still add a row that CancelAllQueued would miss.
	holdMu sync.Mutex
	held   bool
}

// Hold makes Enqueue refuse with ErrHeld until Release (ADR-0018). An
// Enqueue already inserting finishes first.
func (q *Queue) Hold() {
	q.holdMu.Lock()
	q.held = true
	q.holdMu.Unlock()
}

func (q *Queue) Release() {
	q.holdMu.Lock()
	q.held = false
	q.holdMu.Unlock()
}

func (q *Queue) isHeld() bool {
	q.holdMu.Lock()
	defer q.holdMu.Unlock()
	return q.held
}

func NewQueue(d Deps) *Queue {
	if d.Timeout <= 0 {
		d.Timeout = 60 * time.Minute
	}
	if d.Keep <= 0 {
		d.Keep = 20
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Queue{
		d:        d,
		wake:     make(chan struct{}, 1),
		running:  map[int64]*activeRun{},
		srcLocks: map[int64]chan struct{}{},
	}
}

func logf(format string, args ...any) { log.Printf("deployments: "+format, args...) }

// Run settles Deployments a restart interrupted, then works the queue until
// ctx is done. It returns after a Deploy step in progress finishes.
func (q *Queue) Run(ctx context.Context) {
	bg := context.WithoutCancel(ctx)
	if _, err := q.d.Store.FailRunning(bg, reasonRestarted, q.d.Now()); err != nil {
		logf("fail running deployments: %v", err)
	}
	for ctx.Err() == nil {
		d, ok, err := q.d.Store.NextQueued(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logf("next queued: %v", err)
				sleep(ctx, retryDelay)
			}
			continue
		}
		if !ok {
			select {
			case <-q.wake:
			case <-ctx.Done():
			}
			continue
		}
		if err := q.processSafely(ctx, d); err != nil {
			logf("deployment %d of compose %d: %v", d.Number, d.ComposeID, err)
			sleep(ctx, retryDelay)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// Enqueue queues a Deployment, or returns the Compose's already queued one when
// the request asks for the same. A request for other options is refused with a
// QueuedConflictError: reusing the queued one would run or drop a wipe of the
// volumes the caller did not ask for.
func (q *Queue) Enqueue(ctx context.Context, composeID int64, r Request) (Deployment, bool, error) {
	switch r.Mode {
	case ModeDeploy, ModeBuild, ModeSkipBuild:
	default:
		return Deployment{}, false, fmt.Errorf("unknown deployment mode %q", r.Mode)
	}
	if q.isHeld() {
		return Deployment{}, false, ErrHeld
	}
	if existing, err := queuedFor(ctx, q.d.Store.db, composeID); err == nil {
		return reuse(existing, r)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, false, err
	}
	snapshot, err := q.capture(ctx, composeID, r.Mode, true)
	if err != nil {
		var existing *alreadyQueued
		if errors.As(err, &existing) {
			return reuse(existing.deployment, r)
		}
		return Deployment{}, false, err
	}
	defer snapshot.release()
	q.holdMu.Lock()
	var d Deployment
	var created bool
	if q.held {
		err = ErrHeld
	} else {
		d, created, err = q.d.Store.enqueue(ctx, composeID, r.Mode, r.NoCache, r.FreshVolumes, q.d.Now(), snapshot)
	}
	q.holdMu.Unlock()
	if err != nil || !created {
		discardCaptured(snapshot)
	}
	if err != nil {
		return Deployment{}, false, err
	}
	if !created {
		return reuse(d, r)
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return d, created, nil
}

// Cancel cancels a queued Deployment, or one that is fetching or building. It
// returns once a running one has stopped.
func (q *Queue) Cancel(ctx context.Context, composeID int64, number int) error {
	if found, err := q.cancelRunning(ctx, composeID, number); found {
		return err
	}
	d, err := q.d.Store.Get(ctx, composeID, number)
	if err != nil {
		return err
	}
	if d.Status == StatusQueued {
		if ok, err := q.d.Store.CancelQueuedNumber(ctx, composeID, number, q.d.Now()); err != nil || ok {
			if ok && err == nil {
				if snapshot, err := q.d.Store.snapshot(ctx, d.ID); err == nil {
					discardCaptured(snapshot)
				}
			}
			return err
		}
	}
	// It may have started between the two checks above.
	if found, err := q.cancelRunning(ctx, composeID, number); found {
		return err
	}
	return ErrNotCancelable
}

// cancelRunning cancels the Compose's running Deployment when it is number and
// not yet in its Deploy step, and waits for it to settle.
func (q *Queue) cancelRunning(ctx context.Context, composeID int64, number int) (bool, error) {
	q.mu.Lock()
	rs := q.running[composeID]
	if rs == nil || rs.number != number {
		q.mu.Unlock()
		return false, nil
	}
	if rs.phase == phaseDeploying {
		q.mu.Unlock()
		return true, ErrNotCancelable
	}
	rs.canceled = true
	rs.cancel()
	q.mu.Unlock()
	select {
	case <-rs.done:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// CancelForComposes cancels every queued Deployment of the Composes and every
// running one that is not yet deploying, and waits for those to stop. A
// Deployment in its Deploy step holds the Project gate itself, so it is left
// to finish.
func (q *Queue) CancelForComposes(ctx context.Context, composeIDs []int64) {
	for _, id := range composeIDs {
		queued, loadErr := queuedFor(ctx, q.d.Store.db, id)
		if errors.Is(loadErr, sql.ErrNoRows) {
			continue
		}
		if loadErr != nil {
			logf("load queued for compose %d: %v", id, loadErr)
			continue
		}
		if canceled, err := q.d.Store.CancelQueuedNumber(ctx, id, queued.Number, q.d.Now()); err != nil {
			logf("cancel queued for compose %d: %v", id, err)
		} else if canceled {
			if snapshot, err := q.d.Store.snapshot(ctx, queued.ID); err == nil {
				discardCaptured(snapshot)
			}
		}
	}
	var waits []chan struct{}
	q.mu.Lock()
	for _, id := range composeIDs {
		if rs := q.running[id]; rs != nil && rs.phase != phaseDeploying {
			rs.canceled = true
			rs.cancel()
			waits = append(waits, rs.done)
		}
	}
	q.mu.Unlock()
	for _, w := range waits {
		select {
		case <-w:
		case <-ctx.Done():
			return
		}
	}
}

// CancelAllQueued cancels every queued Deployment of every Compose with
// reason, for the Migration lock (ADR-0018). A running one is left to finish:
// WaitIdle waits for it.
func (q *Queue) CancelAllQueued(ctx context.Context, reason string) (int, error) {
	ids, err := q.d.Store.cancelAllQueued(ctx, reason, q.d.Now())
	for _, id := range ids {
		if snapshot, err := q.d.Store.snapshot(ctx, id); err == nil {
			discardCaptured(snapshot)
		}
	}
	return len(ids), err
}

// WaitIdle returns once no Deployment is running, or when ctx ends.
func (q *Queue) WaitIdle(ctx context.Context) error {
	for {
		if _, _, ok := q.Running(); !ok {
			return nil
		}
		t := time.NewTimer(50 * time.Millisecond)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

// Running reports the Deployment the worker is on, if any.
func (q *Queue) Running() (composeID int64, number int, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, rs := range q.running {
		return id, rs.number, true
	}
	return 0, 0, false
}

// IsRunning reports whether a Deployment of the Compose has started and not
// yet settled.
func (q *Queue) IsRunning(composeID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running[composeID] != nil
}

func (q *Queue) srcLock(composeID int64) chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	l := q.srcLocks[composeID]
	if l == nil {
		l = make(chan struct{}, 1)
		q.srcLocks[composeID] = l
	}
	return l
}

// TryLockSource takes the Compose's Source lock unless a Deployment is
// fetching or building from it. The lock is a semaphore rather than a
// sync.Mutex so the worker can give up waiting when it is canceled.
func (q *Queue) TryLockSource(composeID int64) (unlock func(), ok bool) {
	l := q.srcLock(composeID)
	select {
	case l <- struct{}{}:
		return func() { <-l }, true
	default:
		return nil, false
	}
}

func (q *Queue) lockSource(ctx context.Context, composeID int64) (func(), error) {
	l := q.srcLock(composeID)
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type outcome struct {
	status     Status
	failedStep string
	reason     string
	warnings   []string
}

func failed(step, reason string) outcome {
	return outcome{status: StatusFailed, failedStep: step, reason: reason}
}

// processSafely is process with a panic outside the steps (loading the
// Compose, say) settled as a failed Deployment, so it is not retried forever.
func (q *Queue) processSafely(ctx context.Context, d Deployment) (err error) {
	if q.d.Maintenance != nil {
		release, acquireErr := q.d.Maintenance.Acquire(ctx)
		if acquireErr != nil {
			return acquireErr
		}
		defer release()
	}
	defer func() {
		if r := recover(); r != nil {
			logf("deployment %d of compose %d panicked: %v\n%s", d.Number, d.ComposeID, r, debug.Stack())
			err = q.settleUnstarted(ctx, d, failed("", reasonInternal))
		}
	}()
	return q.process(ctx, d)
}

// process runs one queued Deployment to the end. It returns an error only
// when the row could not be settled, so Run backs off instead of spinning.
func (q *Queue) process(ctx context.Context, d Deployment) error {
	bg := context.WithoutCancel(ctx)
	target, err := q.d.Composes.Target(bg, d.ComposeID)
	if errors.Is(err, sql.ErrNoRows) {
		return q.settleUnstarted(ctx, d, outcome{status: StatusCanceled, reason: reasonComposeDeleted})
	}
	if err != nil {
		return q.settleUnstarted(ctx, d, failed("", firstLine(err.Error())))
	}
	snapshot, err := q.d.Store.snapshot(bg, d.ID)
	if err != nil {
		return q.settleUnstarted(ctx, d, failed("", "could not load deployment snapshot"))
	}
	if snapshot == nil {
		// Older queued rows have no historical inputs to recover. Capture the
		// current configuration once, persist it, and disclose that fallback.
		snapshot, err = q.capture(ctx, d.ComposeID, d.Mode, false)
		if err != nil {
			return q.settleUnstarted(ctx, d, failed("", firstLine(err.Error())))
		}
		snapshot.Legacy = true
		if err := q.d.Store.saveLegacySnapshot(bg, d.ID, snapshot); err != nil {
			snapshot.release()
			discardCaptured(snapshot)
			return q.settleUnstarted(ctx, d, failed("", "could not save legacy deployment snapshot"))
		}
		snapshot.release()
	}
	target = snapshot.Target
	if err := q.d.Composes.CheckDeployable(bg, target); err != nil {
		return q.settleUnstarted(ctx, d, failed("", firstLine(err.Error())))
	}

	j := &job{q: q, ctx: ctx, bg: bg, d: d, target: target, snapshot: snapshot, steps: []string{stepDeploy}}
	if d.Mode == ModeSkipBuild && snapshot.Source == nil {
		j.pre = &outcome{status: StatusFailed, reason: reasonNoSource}
	}
	return j.execute()
}

// settleUnstarted settles a Deployment that never got to run its steps.
func (q *Queue) settleUnstarted(ctx context.Context, d Deployment, o outcome) error {
	bg := context.WithoutCancel(ctx)
	if err := q.d.Store.Start(bg, d.ID, []string{stepDeploy}, q.d.Now()); err != nil {
		if errors.Is(err, ErrNotQueued) {
			return nil
		}
		return err
	}
	return finishWithRetry(ctx, q.d.Store, d.ID, o, q.d.Now())
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// job is one Deployment being run by the worker.
type job struct {
	q        *Queue
	ctx      context.Context // Run's context: shutdown cuts fetch and build
	bg       context.Context // never canceled: settles the row
	tctx     context.Context // ctx plus Cancel plus the timeout
	d        Deployment
	target   composes.DeployTarget
	snapshot *executionSnapshot
	src      *sources.Source
	steps    []string
	pre      *outcome // a failure found while resolving the mode, reported after Start
	rs       *activeRun
	out      io.Writer
	step     string
}

func (j *job) execute() error {
	q := j.q
	rctx, cancel := context.WithCancel(j.ctx)
	defer cancel()
	j.rs = &activeRun{number: j.d.Number, phase: phaseStarting, cancel: cancel, done: make(chan struct{})}
	q.mu.Lock()
	q.running[j.d.ComposeID] = j.rs
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.running, j.d.ComposeID)
		q.mu.Unlock()
		close(j.rs.done)
	}()

	if err := q.d.Store.Start(j.bg, j.d.ID, j.steps, q.d.Now()); err != nil {
		if errors.Is(err, ErrNotQueued) {
			if current, err := q.d.Store.Get(j.bg, j.d.ComposeID, j.d.Number); err == nil && current.Status == StatusCanceled {
				discardCaptured(j.snapshot)
			}
			return nil
		}
		return err
	}
	tctx, tcancel := context.WithTimeout(rctx, q.d.Timeout)
	defer tcancel()
	j.tctx = tctx

	dir := composes.ComposeDir(j.target.Request)
	logFile, err := openLog(dir, j.d.Number)
	var o outcome
	if err != nil {
		j.out = io.Discard
		o = failed("", "could not open the deployment log: "+firstLine(err.Error()))
	} else {
		defer logFile.Close()
		j.out = newRedactingWriter(logFile, j.snapshot)
		o = j.runStepsSafely()
	}
	if o.status == StatusFailed || o.status == StatusCanceled {
		fmt.Fprintf(j.out, "%s: %s\n", o.status, o.reason)
	}
	if j.snapshot != nil && j.snapshot.Legacy {
		o.warnings = append(o.warnings, legacySnapshotWarning)
	}
	if redact, ok := j.out.(*redactingWriter); ok {
		o.reason = redact.redact(o.reason)
		for i, value := range o.warnings {
			o.warnings[i] = redact.redact(value)
		}
		_ = redact.flush()
	}

	finishErr := finishWithRetry(j.ctx, q.d.Store, j.d.ID, o, q.d.Now())
	if finishErr != nil {
		return finishErr
	}
	removed, err := q.d.Store.Prune(j.bg, j.d.ComposeID, q.d.Keep)
	if err != nil {
		logf("prune compose %d: %v", j.d.ComposeID, err)
	}
	removeLogs(dir, removed)
	return finishErr
}

// runStepsSafely settles a panic in a step as a failed Deployment, so the
// worker goes on to the next one instead of taking the panel down. The Project
// gate is released by deploy's own deferred release.
func (j *job) runStepsSafely() (o outcome) {
	defer func() {
		if r := recover(); r != nil {
			logf("deployment %d of compose %d panicked: %v\n%s", j.d.Number, j.d.ComposeID, r, debug.Stack())
			fmt.Fprintf(j.out, "internal error: %v\n", r)
			o = failed(j.step, reasonInternal)
		}
	}()
	return j.runSteps()
}

// runSteps runs the Deployment's steps and returns how it ended.
func (j *job) runSteps() outcome {
	if j.pre != nil {
		return *j.pre
	}
	if j.d.Mode == ModeBuild {
		if o, ok := j.fetchAndBuild(); !ok {
			return o
		}
	}
	return j.deploy()
}

func (j *job) setPhase(p phase) {
	j.q.mu.Lock()
	j.rs.phase = p
	j.q.mu.Unlock()
}

func (j *job) begin(step string) {
	j.step = step
	if err := j.q.d.Store.SetStep(j.bg, j.d.ID, step); err != nil {
		logf("set step: %v", err)
	}
	fmt.Fprintf(j.out, "==> %s\n", step)
}

// interrupted reports a Cancel, the timeout or a shutdown, if one happened.
func (j *job) interrupted() (outcome, bool) {
	j.q.mu.Lock()
	canceled := j.rs.canceled
	j.q.mu.Unlock()
	switch {
	case canceled:
		return outcome{status: StatusCanceled}, true
	case errors.Is(j.tctx.Err(), context.DeadlineExceeded):
		return failed(j.step, reasonTimeout), true
	case j.ctx.Err() != nil:
		return failed(j.step, reasonRestarted), true
	}
	return outcome{}, false
}

// stepFailed classifies a step's error: an interruption wins over the error
// it caused.
func (j *job) stepFailed(err error) outcome {
	if o, ok := j.interrupted(); ok {
		return o
	}
	return failed(j.step, firstLine(err.Error()))
}

// fetchAndBuild runs Fetch and every Build step under the Source lock. It
// returns false with the outcome when the Deployment must stop.
func (j *job) fetchAndBuild() (outcome, bool) {
	q := j.q
	unlock, err := q.lockSource(j.tctx, j.d.ComposeID)
	if err != nil {
		return j.stepFailed(err), false
	}
	defer unlock()

	// The Source is read under its lock, so an edit that held the lock while
	// this Deployment waited is the one built.
	var src sources.Source
	if j.snapshot != nil {
		if j.snapshot.Source == nil {
			return failed("", reasonNoSource), false
		}
		src = *j.snapshot.Source
	} else {
		src, err = q.d.Sources.Get(j.bg, j.d.ComposeID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return failed("", reasonNoSource), false
	}
	if err != nil {
		return failed("", firstLine(err.Error())), false
	}
	j.src = &src
	if src.Kind == sources.KindGit {
		if err := q.d.Store.SetSteps(j.bg, j.d.ID, []string{stepFetch, stepDeploy}); err != nil {
			logf("set steps: %v", err)
		}
		if src.Branch == "" {
			j.step = stepFetch
			return failed(stepFetch, reasonNoBranch), false
		}
	}

	j.setPhase(phaseFetching)
	if o, ok := j.interrupted(); ok {
		return o, false
	}
	if j.src.Kind == sources.KindGit {
		j.begin(stepFetch)
		sourceDir := composes.ComposeDir(j.target.Request)
		if j.snapshot != nil {
			sourceDir = j.snapshot.SourceDir
		}
		ref, err := q.d.Fetcher.Fetch(j.tctx, *j.src, sourceDir, j.out)
		if err != nil {
			return j.stepFailed(err), false
		}
		if redact, ok := j.out.(*redactingWriter); ok {
			ref.Subject = redact.redact(ref.Subject)
		}
		if err := q.d.Store.SetSource(j.bg, j.d.ID, ref.SHA, ref.Subject); err != nil {
			logf("set source: %v", err)
		}
	} else if err := q.d.Store.SetSource(j.bg, j.d.ID, j.src.ZipName, ""); err != nil {
		logf("set source: %v", err)
	}

	j.step = ""
	plan, err := q.d.Composes.PrepareBuild(j.tctx, j.target)
	if err != nil {
		return j.stepFailed(err), false
	}
	if plan.Cleanup != nil {
		defer plan.Cleanup()
	}
	if len(plan.Services) == 0 {
		fmt.Fprintln(j.out, "no Service has build:")
		if err := j.recordMaterialization(src); err != nil {
			return j.stepFailed(err), false
		}
		return outcome{}, true
	}
	var steps []string
	if j.src.Kind == sources.KindGit {
		steps = append(steps, stepFetch)
	}
	for _, svc := range plan.Services {
		steps = append(steps, "build:"+svc)
	}
	steps = append(steps, stepDeploy)
	if err := q.d.Store.SetSteps(j.bg, j.d.ID, steps); err != nil {
		logf("set steps: %v", err)
	}

	j.setPhase(phaseBuilding)
	for _, svc := range plan.Services {
		j.begin("build:" + svc)
		if o, ok := j.interrupted(); ok {
			return o, false
		}
		err := q.d.Builder.Build(j.tctx, plan.Request, composes.BuildOptions{Service: svc, NoCache: j.d.NoCache, Out: j.out})
		if err != nil {
			return j.stepFailed(err), false
		}
	}
	if err := j.recordMaterialization(src); err != nil {
		return j.stepFailed(err), false
	}
	return outcome{}, true
}

func (j *job) recordMaterialization(src sources.Source) error {
	if j.snapshot != nil && src.Kind == sources.KindGit {
		if err := j.q.d.Sources.RecordMaterialization(j.bg, src, filepath.Join(j.snapshot.SourceDir, "src")); err != nil {
			return errors.New("could not record built Source files")
		}
	}
	return nil
}

// deploy waits for the Project gate, then runs the Deploy step. It holds
// nothing but the gate while it runs, and the step is shielded from
// shutdown and Cancel.
func (j *job) deploy() outcome {
	q := j.q
	j.begin(stepDeploy)
	j.setPhase(phaseWaitingGate)
	rel, err := q.d.Gate.Acquire(j.tctx, j.target.ProjectID)
	if err != nil {
		return j.stepFailed(err)
	}
	var once sync.Once
	release := func() { once.Do(rel) }
	defer release() // a panic in RunDeploy must not leak the gate

	// A Delete may have held the gate first; running now would recreate the
	// Compose's directory and start containers for a Compose with no row.
	if _, err := q.d.Composes.Target(j.bg, j.d.ComposeID); err != nil {
		release()
		if errors.Is(err, sql.ErrNoRows) {
			return outcome{status: StatusCanceled, reason: reasonComposeDeleted}
		}
		return failed(stepDeploy, firstLine(err.Error()))
	}
	q.mu.Lock()
	if j.tctx.Err() != nil {
		q.mu.Unlock()
		release()
		o, _ := j.interrupted()
		return o
	}
	j.rs.phase = phaseDeploying
	q.mu.Unlock()

	dctx := context.WithoutCancel(j.ctx)
	if deadline, ok := j.tctx.Deadline(); ok {
		var cancel context.CancelFunc
		dctx, cancel = context.WithDeadline(dctx, deadline)
		defer cancel()
	}
	// A deploy-mode Deployment never builds from a Source: its src/ may be
	// unfetched, and the build would run without the source lock.
	build := j.d.Mode == ModeDeploy
	if build {
		hasSource := j.snapshot != nil && j.snapshot.Source != nil
		var err error
		if j.snapshot == nil {
			_, err = q.d.Sources.Get(j.bg, j.d.ComposeID)
			hasSource = err == nil
		}
		if hasSource {
			build = false
			fmt.Fprintln(j.out, "compose has a source; deploying without building")
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			release()
			return failed(stepDeploy, firstLine(err.Error()))
		}
	}
	warnings, err := q.d.Composes.RunDeploy(dctx, j.target, composes.DeployOptions{
		FreshVolumes: j.d.FreshVolumes,
		Build:        build,
		Out:          j.out,
	})
	release()
	if err != nil {
		o := failed(stepDeploy, firstLine(err.Error()))
		if errors.Is(dctx.Err(), context.DeadlineExceeded) {
			o.reason = reasonTimeout
		}
		var failure *composes.DeployFailure
		if errors.As(err, &failure) && failure.VolumesRemoved {
			o.warnings = append(o.warnings, volumesRemovedWarning)
			fmt.Fprintf(j.out, "warning: %s\n", volumesRemovedWarning)
		}
		return o
	}
	return outcome{status: StatusSucceeded, warnings: warnings}
}

const volumesRemovedWarning = "Named volumes were removed before deployment failed. Their data will not be restored automatically. Restore a backup if you need the previous data."

const legacySnapshotWarning = "This deployment was queued before execution snapshots were available. Its configuration and Source were captured when the worker resumed it."

var (
	_ composes.DeploymentCanceler = (*Queue)(nil)
	_ Composes                    = (*composes.Service)(nil)
	_ Fetcher                     = (*sources.Git)(nil)
	_ Builder                     = composes.ComposeRunner(nil)
)
