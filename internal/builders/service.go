package builders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxNameLength  = 40
	minMemoryMB    = 256
	maxParallelism = 64
	pullTimeout    = 2 * time.Minute
)

var (
	ErrBusy         = errors.New("A build is running on this Builder; try again when it finishes")
	ErrIsDefault    = errors.New("This Builder is the panel default; pick another default first")
	ErrQueued       = errors.New("A queued or running Deployment uses this Builder")
	ErrInvalidImage = errors.New("BuildKit image must be repo[:tag]@sha256:<64 hex digits>")
)

// InUseError refuses deleting a Builder that Composes chose explicitly.
type InUseError struct{ Composes []string }

func (e *InUseError) Error() string {
	return "These Composes use this Builder: " + strings.Join(e.Composes, ", ")
}

// ValidationError names the field a Builder's settings break.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Message }

// StartError is a Builder that could not be brought up for a build.
type StartError struct {
	Name string
	Err  error
}

func (e *StartError) Error() string {
	return fmt.Sprintf("Could not start Builder %s: %v", e.Name, e.Err)
}
func (e *StartError) Unwrap() error { return e.Err }

// EngineError is Docker refusing a change to a Builder, in Docker's words.
type EngineError struct{ Err error }

func (e *EngineError) Error() string { return "Docker refused: " + e.Err.Error() }
func (e *EngineError) Unwrap() error { return e.Err }

// refused wraps a Docker error so the operator sees Docker's message.
func refused(err error) error {
	if err == nil {
		return nil
	}
	return &EngineError{Err: err}
}

// PullError is a BuildKit image the panel could not pull.
type PullError struct {
	Ref string
	Err error
}

func (e *PullError) Error() string { return fmt.Sprintf("Could not pull %s: %v", e.Ref, e.Err) }
func (e *PullError) Unwrap() error { return e.Err }

// Deployments reports whether a queued or running Deployment will build on a
// Builder; deployments.Store implements it.
type Deployments interface {
	UsesBuilder(ctx context.Context, id int64) (bool, error)
}

type Deps struct {
	Store       *Store
	Engine      Engine
	Deployments Deployments
	Now         func() time.Time
}

// runtime is a Builder's in-memory state: how many builds hold it, and a
// channel that is open while an exclusive change (re-create, image switch,
// prune, delete) runs. Builds wait for the change; changes refuse while other
// builds hold it. config serialises everything that reads the stored row and
// then changes the container to match, so a bring-up never undoes an edit.
type runtime struct {
	busy     int
	changing chan struct{}
	config   sync.Mutex
}

type Service struct {
	d     Deps
	mu    sync.Mutex
	state map[int64]*runtime
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, state: map[int64]*runtime{}}
}

func (s *Service) runtimeOf(id int64) *runtime {
	r, ok := s.state[id]
	if !ok {
		r = &runtime{}
		s.state[id] = r
	}
	return r
}

// config locks Builder id's container changes; the caller unlocks it.
func (s *Service) config(id int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &s.runtimeOf(id).config
	return m
}

func (s *Service) busy(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeOf(id).busy
}

// exclusive runs fn while no build holds the Builder, making new builds wait
// until it returns; it returns ErrBusy when a build holds it.
func (s *Service) exclusive(id int64, fn func() error) error {
	s.mu.Lock()
	r := s.runtimeOf(id)
	if r.busy > 0 || r.changing != nil {
		s.mu.Unlock()
		return ErrBusy
	}
	done := make(chan struct{})
	r.changing = done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		r.changing = nil
		s.mu.Unlock()
		close(done)
	}()
	return fn()
}

// claimSwitch marks an image switch as an exclusive change when the caller's
// own hold is the only one, so new holders wait for it. It returns false
// when another build holds the Builder; the caller then keeps the old image.
func (s *Service) claimSwitch(id int64) (done func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runtimeOf(id)
	if r.busy != 1 || r.changing != nil {
		return nil, false
	}
	ch := make(chan struct{})
	r.changing = ch
	return func() {
		s.mu.Lock()
		r.changing = nil
		s.mu.Unlock()
		close(ch)
	}, true
}

// hold waits out an exclusive change, then counts one more build on the
// Builder.
func (s *Service) hold(ctx context.Context, id int64) (release func(), err error) {
	for {
		s.mu.Lock()
		r := s.runtimeOf(id)
		if r.changing == nil {
			r.busy++
			s.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					s.mu.Lock()
					r.busy--
					s.mu.Unlock()
				})
			}, nil
		}
		wait := r.changing
		s.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Describe is a Builder's name and limits as the log and the UI show them.
func Describe(b Builder) string {
	text := fmt.Sprintf("%s (%g CPU, %s", b.Name, float64(b.CPUsMilli)/1000, memoryText(b.MemoryMB))
	if b.MaxParallelism > 0 {
		text += fmt.Sprintf(", %d parallel steps", b.MaxParallelism)
	}
	return text + ")"
}

// memoryText is N GB for whole gigabytes, else N MB.
func memoryText(mb int) string {
	if mb%1024 == 0 {
		return fmt.Sprintf("%d GB", mb/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

func (s *Service) image(ctx context.Context) (string, error) {
	setting, err := s.d.Store.BuildKitImage(ctx)
	return EffectiveImage(setting), err
}

// Acquire brings Builder id up for a build and holds it until release: its
// container exists, runs and matches the stored limits and, when no other
// build holds it, the BuildKit image setting. It logs which Builder builds.
// DockerDefault needs nothing and returns an empty name.
func (s *Service) Acquire(ctx context.Context, id int64, out io.Writer) (string, func(), error) {
	if id == DockerDefault {
		fmt.Fprintln(out, "Builder: Docker default (no limits)")
		return "", func() {}, nil
	}
	release, err := s.hold(ctx, id)
	if err != nil {
		return "", nil, err
	}
	b, err := s.bringUp(ctx, id, out)
	if err != nil {
		release()
		return "", nil, err
	}
	fmt.Fprintln(out, "Builder: "+Describe(b))
	return HostName(id), release, nil
}

// Start brings Builder id up without a build.
func (s *Service) Start(ctx context.Context, id int64) (Builder, error) {
	release, err := s.hold(ctx, id)
	if err != nil {
		return Builder{}, err
	}
	defer release()
	return s.bringUp(ctx, id, io.Discard)
}

func (s *Service) bringUp(ctx context.Context, id int64, out io.Writer) (Builder, error) {
	lock := s.config(id)
	lock.Lock()
	defer lock.Unlock()
	b, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return Builder{}, err
	}
	want, err := s.image(ctx)
	if err != nil {
		return Builder{}, err
	}
	err = s.converge(ctx, b, want, out)
	if err != nil {
		_ = s.d.Store.SetLastError(ctx, id, err.Error())
		return Builder{}, &StartError{Name: b.Name, Err: err}
	}
	if b.LastError != "" {
		if err := s.d.Store.SetLastError(ctx, id, ""); err != nil {
			return Builder{}, err
		}
		b.LastError = ""
	}
	return b, nil
}

func (s *Service) converge(ctx context.Context, b Builder, want string, out io.Writer) error {
	insp, err := s.d.Engine.Inspect(ctx, HostName(b.ID))
	if err != nil {
		return err
	}
	spec := SpecFor(b, want)
	if insp.State == StateMissing || insp.Image == want {
		return s.d.Engine.Ensure(ctx, spec)
	}
	if done, ok := s.claimSwitch(b.ID); ok {
		defer done()
		fmt.Fprintf(out, "Builder %s: switching BuildKit image to %s\n", b.Name, want)
		return s.d.Engine.Recreate(ctx, spec)
	}
	fmt.Fprintf(out, "Builder %s still runs %s; it switches to %s once its other builds finish\n", b.Name, insp.Image, want)
	spec.Image = insp.Image
	return s.d.Engine.Ensure(ctx, spec)
}

func (s *Service) validate(ctx context.Context, b *Builder) error {
	b.Name = strings.TrimSpace(b.Name)
	if n := utf8.RuneCountInString(b.Name); n < 1 || n > maxNameLength {
		return &ValidationError{"name", "Name must be 1–40 characters"}
	}
	if strings.EqualFold(b.Name, "Docker default") {
		return &ValidationError{"name", "Docker default is the built-in Builder's name"}
	}
	list, err := s.d.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, other := range list {
		if other.ID != b.ID && strings.EqualFold(other.Name, b.Name) {
			return &ValidationError{"name", "A Builder named " + other.Name + " already exists"}
		}
	}
	cpus, memory, err := s.d.Engine.Capacity(ctx)
	if err != nil {
		return err
	}
	if b.CPUsMilli < 100 || b.CPUsMilli%100 != 0 || b.CPUsMilli > cpus*1000 {
		return &ValidationError{"cpus", fmt.Sprintf("CPU must be between 0.1 and %d, in steps of 0.1", cpus)}
	}
	if maxMB := int(memory >> 20); b.MemoryMB < minMemoryMB || b.MemoryMB > maxMB {
		return &ValidationError{"memoryMb", fmt.Sprintf("Memory must be between %d MB and %d MB", minMemoryMB, maxMB)}
	}
	if b.MaxParallelism < 0 || b.MaxParallelism > maxParallelism {
		return &ValidationError{"maxParallelism", "Max parallel steps must be empty or 1–64"}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, b Builder) (Builder, error) {
	b.ID = 0
	if err := s.validate(ctx, &b); err != nil {
		return Builder{}, err
	}
	return s.d.Store.Create(ctx, b, s.d.Now())
}

// Update saves a Builder's name and limits. A created container gets the new
// CPU and memory first, even mid-build; a new max parallel steps re-creates
// it, which waits for no build to hold it. When Docker refuses, the row is
// left as it was.
func (s *Service) Update(ctx context.Context, b Builder) (Builder, error) {
	lock := s.config(b.ID)
	lock.Lock()
	defer lock.Unlock()
	old, err := s.d.Store.Get(ctx, b.ID)
	if err != nil {
		return Builder{}, err
	}
	if err := s.validate(ctx, &b); err != nil {
		return Builder{}, err
	}
	insp, err := s.d.Engine.Inspect(ctx, HostName(b.ID))
	if err != nil {
		return Builder{}, err
	}
	if insp.State != StateMissing {
		switch {
		case b.MaxParallelism != old.MaxParallelism:
			want, err := s.image(ctx)
			if err != nil {
				return Builder{}, err
			}
			err = s.exclusive(b.ID, func() error { return refused(s.d.Engine.Recreate(ctx, SpecFor(b, want))) })
			if err != nil {
				return Builder{}, err
			}
		case b.CPUsMilli != old.CPUsMilli || b.MemoryMB != old.MemoryMB:
			if err := s.d.Engine.Ensure(ctx, SpecFor(b, insp.Image)); err != nil {
				return Builder{}, refused(err)
			}
		}
	}
	if err := s.d.Store.Update(ctx, b, s.d.Now()); err != nil {
		return Builder{}, err
	}
	return s.d.Store.Get(ctx, b.ID)
}

// Delete removes a Builder and its cache. It refuses the panel default, one a
// Compose chose and one a queued or running Deployment will build on.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.d.Store.Get(ctx, id); err != nil {
		return err
	}
	def, err := s.d.Store.DefaultID(ctx)
	if err != nil {
		return err
	}
	if def == id {
		return ErrIsDefault
	}
	used, err := s.d.Store.UsedBy(ctx, id)
	if err != nil {
		return err
	}
	if len(used) > 0 {
		return &InUseError{Composes: used}
	}
	queued, err := s.d.Deployments.UsesBuilder(ctx, id)
	if err != nil {
		return err
	}
	if queued {
		return ErrQueued
	}
	return s.exclusive(id, func() error {
		if err := s.d.Engine.Remove(ctx, HostName(id)); err != nil {
			return refused(err)
		}
		return s.d.Store.Delete(ctx, id)
	})
}

// SetDefault makes Builder id (DockerDefault included) the panel default.
func (s *Service) SetDefault(ctx context.Context, id int64) error {
	if id != DockerDefault {
		if _, err := s.d.Store.Get(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return ErrUnknownBuilder
		} else if err != nil {
			return err
		}
	}
	return s.d.Store.SetDefault(ctx, id)
}

func (s *Service) Prune(ctx context.Context, id int64) error {
	if _, err := s.d.Store.Get(ctx, id); err != nil {
		return err
	}
	return s.exclusive(id, func() error { return refused(s.d.Engine.Prune(ctx, HostName(id))) })
}

func (s *Service) DiskUsage(ctx context.Context, id int64) (int64, error) {
	if _, err := s.d.Store.Get(ctx, id); err != nil {
		return 0, err
	}
	return s.d.Engine.DiskUsage(ctx, HostName(id))
}

// SetBuildKitImage stores the BuildKit image setting (” for the default).
// A custom image is pulled first; a failed pull keeps the old setting.
// Builders switch to it on their next use, not here.
func (s *Service) SetBuildKitImage(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		if !ValidImageRef(ref) {
			return ErrInvalidImage
		}
		pullCtx, cancel := context.WithTimeout(ctx, pullTimeout)
		defer cancel()
		if err := s.d.Engine.Pull(pullCtx, ref); err != nil {
			return &PullError{Ref: ref, Err: err}
		}
	}
	return s.d.Store.SetBuildKitImage(ctx, ref)
}
