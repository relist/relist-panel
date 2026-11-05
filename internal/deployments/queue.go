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

	"relist-panel/internal/audit"
	"relist-panel/internal/composes"
	"relist-panel/internal/dockerhost"
	"relist-panel/internal/safely"
	"relist-panel/internal/sources"
	"relist-panel/internal/zerodowntime"
)

// Gate serialises work on a Project; the Deploy step holds it. AcquireVolumes
// is the exclusive hold on the Project's volumes, which a fresh-volumes Deploy
// needs on top of the gate. Open Volume reads share the volumes but not the
// gate, so only AcquireVolumes waits for them. Take it after Acquire, never
// before.
type Gate interface {
	Acquire(ctx context.Context, projectID int64) (release func(), err error)
	AcquireVolumes(ctx context.Context, projectID int64) (release func(), err error)
}

type Composes interface {
	Target(ctx context.Context, composeID int64) (composes.DeployTarget, error)
	Capture(ctx context.Context, t composes.DeployTarget) (composes.DeployTarget, error)
	CheckDeployable(ctx context.Context, t composes.DeployTarget) error
	PrepareBuild(ctx context.Context, t composes.DeployTarget) (composes.BuildPlan, error)
	RunDeploy(ctx context.Context, t composes.DeployTarget, opts composes.DeployOptions) ([]string, error)
	RunRollout(ctx context.Context, t composes.DeployTarget, service string, opts composes.RolloutOptions) ([]string, error)
	MarkDeployed(ctx context.Context, t composes.DeployTarget) error
	ZeroDowntimeProblems(ctx context.Context, t composes.DeployTarget) ([]zerodowntime.Problem, error)
}

type Fetcher interface {
	ResolveRevision(ctx context.Context, src sources.Source, composeDir string) (string, error)
	Fetch(ctx context.Context, src sources.Source, composeDir string, out io.Writer) (sources.Ref, error)
}

type Builder interface {
	Build(ctx context.Context, req composes.RunRequest, opts composes.BuildOptions) error
}

// BuilderPool brings a Deployment's Builder up and holds it for the build
// phase; builders.Service implements it. Acquire logs which Builder builds,
// and FailureHint explains a failed build in the operator's terms or is "".
type BuilderPool interface {
	Acquire(ctx context.Context, id int64, out io.Writer) (name string, release func(), err error)
	FailureHint(ctx context.Context, id int64, err error) string
}

type MaintenanceGate interface {
	Acquire(context.Context) (func(), error)
}

type Deps struct {
	Maintenance MaintenanceGate
	Store       *Store
	Sources     *sources.Store
	Composes    Composes
	// Policy checks every Deployment against the Compose policy; nil turns
	// the check off (package tests).
	Policy   Policy
	Fetcher  Fetcher
	Builder  Builder
	Builders BuilderPool // nil builds everything on Docker default, unlogged
	Gate     Gate
	Mounts   MountLister   // nil turns off snapshot collection
	Timeout  time.Duration // 60 * time.Minute in production
	Keep     int           // 20
	Now      func() time.Time
	// OnSucceeded, if set, is called in its own goroutine once a Deployment
	// is recorded as succeeded, outside the Project gate.
	OnSucceeded func(composeID int64)
}

type Request struct {
	Mode                  Mode
	NoCache, FreshVolumes bool
	// UserID and Approver are who asked; ConfigHash is the config hash an
	// approver was shown, which approves that config as it queues
	// (ADR-0032).
	UserID     int64
	Approver   bool
	ConfigHash string
}

// Policy evaluates a Compose's snapshot against the Compose policy and
// records approvals; composes.Service implements it.
type Policy interface {
	EvaluatePolicy(ctx context.Context, t composes.DeployTarget) (composes.PolicyResult, error)
	Approve(ctx context.Context, composeID int64, hash string, userID int64) error
}

// PolicyError refuses a Deployment whose config violates the Compose policy
// and is not approved. Approver says whether the requester may see the hash.
type PolicyError struct {
	Result   composes.PolicyResult
	Approver bool
}

func (e *PolicyError) Error() string {
	return "This Compose needs an approver's approval before it can deploy"
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

// ZeroDowntimeError refuses a Deployment whose zero-downtime Services can't
// be rolled out (ADR-0023); nothing is queued.
type ZeroDowntimeError struct{ Problems []zerodowntime.Problem }

func (e *ZeroDowntimeError) Error() string {
	return "zero-downtime deploy is on for services that can't be rolled out"
}

// ErrHeld refuses a Deployment while a server Migration holds the queue.
var ErrHeld = errors.New("deployments are paused while this server is being migrated")

const (
	reasonRestarted      = "panel restarted"
	reasonTimeout        = "timeout"
	reasonComposeDeleted = "compose deleted"
	reasonNoSource       = "compose has no source"
	reasonNoBranch       = "no branch configured"
	reasonNoKey          = "Choose an SSH key for this Source"
	reasonInternal       = "internal error"

	stepFetch  = "fetch"
	stepDeploy = "deploy"
)

// retryDelay is how long Run backs off after an error; tests shorten it.
var retryDelay = time.Second

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
	// Collected in the background: a hung daemon must not hold up the first
	// Deployment. Each collection takes the Project gate and volumes hold, so it
	// is safe beside the worker; Run returns only once it has ended.
	var boot sync.WaitGroup
	boot.Add(1)
	go func() {
		defer boot.Done()
		safely.Do("collect snapshots at boot", func() { q.collectAllSnapshots(ctx) })
	}()
	defer boot.Wait()
	paused := false // the paused Docker state is already logged for this period
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
			paused = false
			select {
			case <-q.wake:
			case <-ctx.Done():
			}
			continue
		}
		err = q.processSafely(ctx, d)
		if errors.Is(err, dockerhost.ErrMaintenanceBlocked) {
			// The Deployment stays queued until an operator resumes Docker
			// operations; say so once per blocked period, not every retry.
			if !paused {
				logf("deployment %d of compose %d waits: %v", d.Number, d.ComposeID, err)
				paused = true
			}
			sleep(ctx, retryDelay)
			continue
		}
		paused = false
		if err != nil {
			logf("deployment %d of compose %d: %v", d.Number, d.ComposeID, err)
			sleep(ctx, retryDelay)
		}
	}
}

// collectAllSnapshots clears the source snapshots earlier runs left behind.
func (q *Queue) collectAllSnapshots(ctx context.Context) {
	if q.d.Mounts == nil {
		return
	}
	rows, err := q.d.Store.db.QueryContext(ctx, `SELECT id FROM composes`)
	if err != nil {
		logf("list composes for snapshot collection: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		logf("list composes for snapshot collection: %v", err)
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		q.collectSnapshots(ctx, id)
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
	if err := q.admit(ctx, composeID, r, snapshot); err != nil {
		discardCaptured(snapshot)
		return Deployment{}, false, err
	}
	if !r.FreshVolumes && snapshot.Files != nil && len(snapshot.Files.ZeroDowntime) > 0 {
		problems, err := q.d.Composes.ZeroDowntimeProblems(ctx, snapshot.Target)
		if err == nil && len(problems) > 0 {
			err = &ZeroDowntimeError{Problems: problems}
		}
		if err != nil {
			discardCaptured(snapshot)
			return Deployment{}, false, err
		}
	}
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

// Busy reports whether a Deployment is running or the queue is held for a
// Migration, when other heavy work on the host should wait (ADR-0021).
func (q *Queue) Busy() bool {
	if q.isHeld() {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.running) > 0
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

	var buildServices, zeroDowntime []string
	if snapshot.Files != nil {
		buildServices, zeroDowntime = snapshot.Files.Services, snapshot.Files.ZeroDowntime
	}
	p := planDeployment(planInput{mode: d.Mode, noCache: d.NoCache, freshVolumes: d.FreshVolumes, source: snapshot.Source, buildServices: buildServices, zeroDowntime: zeroDowntime})
	steps := []string{stepDeploy}
	if names := p.stepNames(); len(names) > 0 {
		steps = names
	}
	j := &job{q: q, ctx: ctx, bg: bg, d: d, target: target, snapshot: snapshot, steps: steps, plan: p}
	err = j.execute()
	if j.settled {
		// After execute returns, so a Cancel or Delete that holds the Project
		// gate while waiting for this Deployment is not left waiting on it.
		q.collectSnapshots(bg, d.ComposeID)
	}
	return err
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
	plan     plan
	rs       *activeRun
	out      io.Writer
	step     string
	settled  bool  // the final outcome is recorded
	builder  int64 // the Builder acquired for this run, 0 for Docker default
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
	if o.status == StatusSucceeded && q.d.OnSucceeded != nil {
		go q.d.OnSucceeded(j.d.ComposeID)
	}
	removed, err := q.d.Store.Prune(j.bg, j.d.ComposeID, q.d.Keep)
	if err != nil {
		logf("prune compose %d: %v", j.d.ComposeID, err)
	}
	removeLogs(dir, removed)
	j.settled = true
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

// runSteps runs the Deployment's planned steps and returns how it ended.
func (j *job) runSteps() outcome {
	p := j.plan
	if p.fail != nil {
		return *p.fail
	}
	if j.q.d.Builders != nil && builds(p, j.target.Snapshot) {
		// The Builder is held from before the first build until up returns,
		// since up --build builds too.
		j.builder = j.target.Snapshot.BuilderID
		name, release, err := j.q.d.Builders.Acquire(j.tctx, j.builder, j.out)
		if err != nil {
			return j.stepFailed(err)
		}
		defer release()
		j.target.Request.Builder = name
	}
	if p.usesSource || len(p.prepare) > 0 {
		if o, ok := j.runPrepare(p); !ok {
			return o
		}
	}
	return j.runApply(p.apply)
}

// builds reports whether the Deployment runs a build: a Service has build:
// and a step builds it, by itself or through up --build.
func builds(p plan, snapshot *composes.ExecutionSnapshot) bool {
	if snapshot == nil || len(snapshot.Services) == 0 {
		return false
	}
	for _, s := range p.prepare {
		if s.kind == kindBuild {
			return true
		}
	}
	for _, s := range p.apply {
		if s.build {
			return true
		}
	}
	return false
}

// hint logs the Builder's explanation of a failed build, if it has one.
func (j *job) hint(err error) {
	if j.q.d.Builders == nil {
		return
	}
	if h := j.q.d.Builders.FailureHint(j.bg, j.builder, err); h != "" {
		fmt.Fprintln(j.out, h)
	}
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

// beginStep begins a planned step and logs its note.
func (j *job) beginStep(s step) {
	j.begin(s.name)
	if s.note != "" {
		fmt.Fprintln(j.out, s.note)
	}
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

// runPrepare runs the prepare steps (Fetch and every Build) under the Source
// lock. It returns false with the outcome when the Deployment must stop.
func (j *job) runPrepare(p plan) (outcome, bool) {
	q := j.q
	unlock, err := q.lockSource(j.tctx, j.d.ComposeID)
	if err != nil {
		return j.stepFailed(err), false
	}
	defer unlock()

	// The plan only uses a Source the snapshot holds, so an edit that held the
	// lock while this Deployment waited is not the one built.
	src := *j.snapshot.Source
	j.src = &src

	j.setPhase(phaseFetching)
	if o, ok := j.interrupted(); ok {
		return o, false
	}
	if src.Kind != sources.KindGit {
		if err := q.d.Store.SetSource(j.bg, j.d.ID, src.ZipName, ""); err != nil {
			logf("set source: %v", err)
		}
	}

	var build *composes.BuildPlan
	defer func() {
		if build != nil && build.Cleanup != nil {
			build.Cleanup()
		}
	}()
	prepareBuild := func() (outcome, bool) {
		// A build runs with what the file asks for too (privileged, host
		// network), so the policy is checked before the first one.
		if err := j.checkPolicy(); err != nil {
			return j.stepFailed(err), false
		}
		j.step = ""
		plan, err := q.d.Composes.PrepareBuild(j.tctx, j.target)
		if err != nil {
			return j.stepFailed(err), false
		}
		build = &plan
		return outcome{}, true
	}
	for _, s := range p.prepare {
		switch s.kind {
		case kindFetch:
			if o, ok := j.runFetch(s); !ok {
				return o, false
			}
		case kindBuild:
			if build == nil {
				if o, ok := prepareBuild(); !ok {
					return o, false
				}
				j.setPhase(phaseBuilding)
			}
			j.beginStep(s)
			if o, ok := j.interrupted(); ok {
				return o, false
			}
			err := q.d.Builder.Build(j.tctx, build.Request, composes.BuildOptions{Service: s.service, NoCache: s.noCache, Out: j.out})
			if err != nil {
				j.hint(err)
				return j.stepFailed(err), false
			}
		}
	}
	if build == nil {
		if o, ok := prepareBuild(); !ok {
			return o, false
		}
	}
	if p.afterPrepare != "" {
		fmt.Fprintln(j.out, p.afterPrepare)
	}
	if err := j.recordMaterialization(src); err != nil {
		return j.stepFailed(err), false
	}
	return outcome{}, true
}

// runFetch fetches the Git Source into the snapshot's directory and records
// the commit it got.
func (j *job) runFetch(s step) (outcome, bool) {
	q := j.q
	j.beginStep(s)
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

// runApply waits for the Project gate (and, when a step deletes volumes, the
// exclusive volumes hold), then runs the apply steps. It holds nothing but
// those while they run, and the steps are shielded from shutdown and Cancel.
func (j *job) runApply(steps []step) outcome {
	q := j.q
	j.beginStep(steps[0])
	j.setPhase(phaseWaitingGate)
	rel, err := q.d.Gate.Acquire(j.tctx, j.target.ProjectID)
	if err != nil {
		return j.stepFailed(err)
	}
	if freshVolumes(steps) {
		// The step deletes the Compose's volumes, so open Volume reads must
		// end first. Waiting here keeps the gate, as a Delete would.
		relVolumes, err := q.d.Gate.AcquireVolumes(j.tctx, j.target.ProjectID)
		if err != nil {
			rel()
			return j.stepFailed(err)
		}
		relGate := rel
		rel = func() { relVolumes(); relGate() }
	}
	var once sync.Once
	release := func() { once.Do(rel) }
	defer release() // a panic in a step must not leak the gate

	// A Delete may have held the gate first; running now would recreate the
	// Compose's directory and start containers for a Compose with no row.
	if _, err := q.d.Composes.Target(j.bg, j.d.ComposeID); err != nil {
		release()
		if errors.Is(err, sql.ErrNoRows) {
			return outcome{status: StatusCanceled, reason: reasonComposeDeleted}
		}
		return failed(j.step, firstLine(err.Error()))
	}
	// Checked again as late as possible before volumes are wiped, a rollout
	// starts or up runs.
	if err := j.checkPolicy(); err != nil {
		release()
		return j.stepFailed(err)
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
	var warnings []string
	rolledOut := false
	for i, s := range steps {
		if i > 0 {
			j.beginStep(s)
		}
		var stepWarnings []string
		var err error
		switch s.kind {
		case kindDeploy:
			stepWarnings, err = j.runDeploy(dctx, s)
		case kindRollout:
			rolledOut = true
			stepWarnings, err = j.runRollout(dctx, s)
		}
		if err != nil {
			if s.build {
				j.hint(err)
			}
			for _, rest := range steps[i+1:] {
				if rest.kind == kindRollout {
					fmt.Fprintf(j.out, "not deployed: %s\n", rest.service)
				}
			}
			release()
			o := failed(s.name, firstLine(err.Error()))
			// Warnings already raised still describe what this Deployment did.
			o.warnings = append(append(o.warnings, warnings...), stepWarnings...)
			if errors.Is(dctx.Err(), context.DeadlineExceeded) {
				// The reason becomes "timeout"; the log keeps what the step
				// did about it, such as a container it could not remove.
				fmt.Fprintf(j.out, "error: %s\n", firstLine(err.Error()))
				o.reason = reasonTimeout
			}
			var failure *composes.DeployFailure
			if errors.As(err, &failure) && failure.VolumesRemoved {
				o.warnings = append(o.warnings, volumesRemovedWarning)
				fmt.Fprintf(j.out, "warning: %s\n", volumesRemovedWarning)
			}
			return o
		}
		warnings = append(warnings, stepWarnings...)
	}
	// RunDeploy leaves the deploy time to the rollouts when it excluded any.
	if rolledOut {
		if err := q.d.Composes.MarkDeployed(j.bg, j.target); err != nil {
			release()
			return outcome{status: StatusFailed, failedStep: j.step, reason: "could not record deploy time", warnings: warnings}
		}
	}
	release()
	return outcome{status: StatusSucceeded, warnings: warnings}
}

// freshVolumes reports whether any step deletes the Compose's volumes.
func freshVolumes(steps []step) bool {
	for _, s := range steps {
		if s.freshVolumes {
			return true
		}
	}
	return false
}

// runDeploy writes the Compose's files and runs up.
func (j *job) runDeploy(dctx context.Context, s step) ([]string, error) {
	return j.q.d.Composes.RunDeploy(dctx, j.target, composes.DeployOptions{
		FreshVolumes: s.freshVolumes,
		Build:        s.build,
		Exclude:      s.exclude,
		Out:          j.out,
	})
}

// runRollout replaces one Service's Container without a gap (ADR-0023). Its
// health wait ends at the Deployment's deadline.
func (j *job) runRollout(dctx context.Context, s step) ([]string, error) {
	deadline, _ := dctx.Deadline()
	return j.q.d.Composes.RunRollout(dctx, j.target, s.service, composes.RolloutOptions{
		Build:    s.build,
		Deadline: deadline,
		Out:      j.out,
	})
}

const volumesRemovedWarning = "Named volumes were removed before deployment failed. Their data will not be restored automatically. Restore a backup if you need the previous data."

const legacySnapshotWarning = "This deployment was queued before execution snapshots were available. Its configuration and Source were captured when the worker resumed it."

var (
	_ composes.DeploymentCanceler = (*Queue)(nil)
	_ Composes                    = (*composes.Service)(nil)
	_ Fetcher                     = (*sources.Git)(nil)
	_ Builder                     = composes.ComposeRunner(nil)
)

// admit checks the captured snapshot against the Compose policy. A config
// that passes, or is approved, queues. An approver who sends the hash they
// were shown approves it here. A config that cannot be resolved before a
// git Source is fetched queues: the check after the fetch decides.
func (q *Queue) admit(ctx context.Context, composeID int64, r Request, snapshot *executionSnapshot) error {
	if q.d.Policy == nil {
		return nil
	}
	result, err := q.d.Policy.EvaluatePolicy(ctx, snapshot.Target)
	if err != nil {
		if snapshot.Source != nil && snapshot.Source.Kind == sources.KindGit && r.Mode == ModeBuild {
			return nil
		}
		return err
	}
	if result.Passes() {
		return nil
	}
	if r.Approver && r.ConfigHash != "" && r.ConfigHash == result.Hash {
		if err := q.d.Policy.Approve(ctx, composeID, result.Hash, r.UserID); err != nil {
			return err
		}
		audit.Detail(ctx, "approved", true)
		return nil
	}
	return &PolicyError{Result: result, Approver: r.Approver}
}

// checkPolicy evaluates the Deployment's own snapshot against the Compose
// policy. A config that violates and is not approved fails the step, and so
// does one that cannot be evaluated.
func (j *job) checkPolicy() error {
	if j.q.d.Policy == nil {
		return nil
	}
	result, err := j.q.d.Policy.EvaluatePolicy(j.tctx, j.target)
	if err != nil {
		return fmt.Errorf("policy: could not check the config: %w", err)
	}
	if result.Passes() {
		return nil
	}
	reasons := make([]string, 0, len(result.Violations))
	for _, v := range result.Violations {
		reasons = append(reasons, v.Service+": "+v.Message)
	}
	return fmt.Errorf("policy: this Compose needs an approver's approval: %s", strings.Join(reasons, "; "))
}
