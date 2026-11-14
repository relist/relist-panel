package databases

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"relist-panel/internal/slug"
)

// A Database's status, as the list and the Database page show it.
const (
	StatusCreating      = "creating"
	StatusRunning       = "running"
	StatusStarting      = "starting"
	StatusUnhealthy     = "unhealthy"
	StatusStopped       = "stopped"
	StatusDrift         = "drift"
	StatusError         = "error"
	StatusVolumeMissing = "volume_missing"
)

var (
	ErrInvalidName = errors.New("name must be 1 to 63 characters")
	ErrConfirm     = errors.New("type the Database's name to confirm")
	ErrLocked      = errors.New("a server Migration is running; Databases cannot change until it ends")
)

// Deps wires the Service. Locked reports the server Migration lock (nil =
// never locked).
type Deps struct {
	Store  *Store
	Host   Host
	Locked func(ctx context.Context) bool
	Now    func() time.Time
}

type CreateInput struct {
	Name, Engine, Version string
	MemoryMB              int64
	CPUs                  float64
}

// View is a Database with its live status.
type View struct {
	Database
	Status   string
	Warnings []string
}

// Service runs the panel's Databases. The database is the source of truth;
// Reconcile makes the host match it (ADR-0039).
type Service struct {
	d Deps

	mu       sync.Mutex
	locks    map[int64]*sync.Mutex
	inflight map[int64]bool
	wg       sync.WaitGroup
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, locks: map[int64]*sync.Mutex{}, inflight: map[int64]bool{}}
}

func (s *Service) locked(ctx context.Context) bool {
	return s.d.Locked != nil && s.d.Locked(ctx)
}

// lock takes the per-Database mutex and returns its unlock.
func (s *Service) lock(id int64) func() {
	s.mu.Lock()
	m, ok := s.locks[id]
	if !ok {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *Service) setInflight(id int64, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.inflight[id] = true
	} else {
		delete(s.inflight, id)
	}
}

func (s *Service) isInflight(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight[id]
}

// Wait blocks until background work has finished.
func (s *Service) Wait() { s.wg.Wait() }

func newPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 63 {
		return "", ErrInvalidName
	}
	return name, nil
}

// slugFree reports whether no other Database has slug. The fallback slug "x"
// (a name with no ASCII letters or digits) gets no alias, so it never clashes.
func (s *Service) slugFree(ctx context.Context, sl string, self int64) (bool, error) {
	if sl == "x" {
		return true, nil
	}
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range all {
		if o.ID != self && o.Slug == sl {
			return false, nil
		}
	}
	return true, nil
}

// Create stores a Database and starts bringing its container up in the
// background; follow it with Get until its status leaves "creating".
func (s *Service) Create(ctx context.Context, in CreateInput) (Database, error) {
	if s.locked(ctx) {
		return Database{}, ErrLocked
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Database{}, err
	}
	if _, err := Lookup(in.Engine, in.Version); err != nil {
		return Database{}, err
	}
	if in.MemoryMB < 0 || in.CPUs < 0 {
		return Database{}, errors.New("limits cannot be negative")
	}
	sl := slug.Slugify(name)
	if free, err := s.slugFree(ctx, sl, 0); err != nil {
		return Database{}, err
	} else if !free {
		return Database{}, ErrDuplicateName
	}
	pw, err := newPassword()
	if err != nil {
		return Database{}, err
	}
	d, err := s.d.Store.Insert(ctx, Database{
		Name: name, Slug: sl, Engine: in.Engine, Version: in.Version, Password: pw,
		MemoryMB: in.MemoryMB, CPUs: in.CPUs, Desired: DesiredRunning,
	})
	if err != nil {
		return Database{}, err
	}
	s.setInflight(d.ID, true)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.setInflight(d.ID, false)
		unlock := s.lock(d.ID)
		defer unlock()
		_, _ = s.reconcile(context.WithoutCancel(ctx), d)
	}()
	return d, nil
}

// specFor builds the container spec for d. The hash covers everything but the
// alias, so a rename never recreates the container.
func specFor(d Database) (ContainerSpec, Engine, error) {
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		return ContainerSpec{}, nil, err
	}
	spec := ContainerSpec{
		Name: ContainerName(d.ID), Volume: VolumeName(d.ID),
		Image: eng.Image(d.Version), DataPath: eng.DataPath(d.Version),
		Env: eng.Env(d.Password), Cmd: eng.Cmd(), Healthcheck: eng.Healthcheck(),
		Alias:       Alias(d.Slug),
		MemoryBytes: d.MemoryMB * 1024 * 1024,
		NanoCPUs:    int64(d.CPUs * 1e9),
	}
	pwHash := sha256.Sum256([]byte(d.Password))
	hashInput := struct {
		Image, DataPath       string
		Env, Cmd, Healthcheck []string
		MemoryBytes, NanoCPUs int64
	}{spec.Image, spec.DataPath, eng.Env(hex.EncodeToString(pwHash[:])), spec.Cmd, spec.Healthcheck, spec.MemoryBytes, spec.NanoCPUs}
	raw, err := json.Marshal(hashInput)
	if err != nil {
		return ContainerSpec{}, nil, err
	}
	sum := sha256.Sum256(raw)
	spec.Labels = map[string]string{
		IDLabel:   fmt.Sprint(d.ID),
		HashLabel: hex.EncodeToString(sum[:]),
	}
	return spec, eng, nil
}

// record stores the outcome of one operation on d.
func (s *Service) record(ctx context.Context, d Database, err error) {
	ctx = context.WithoutCancel(ctx)
	switch {
	case err != nil && !errors.Is(err, ErrLocked):
		_ = s.d.Store.SetLastError(ctx, d.ID, err.Error())
	case err == nil && d.LastError != "":
		_ = s.d.Store.SetLastError(ctx, d.ID, "")
	}
}

// create (re)creates the container on its volume and honours Desired.
func (s *Service) create(ctx context.Context, d Database, spec ContainerSpec) error {
	if err := s.d.Host.EnsureImage(ctx, spec.Image); err != nil {
		return err
	}
	if err := s.d.Host.Create(ctx, spec); err != nil {
		return err
	}
	if !d.Initialized {
		if err := s.d.Store.SetInitialized(ctx, d.ID); err != nil {
			return err
		}
	}
	if d.Desired == DesiredStopped {
		return s.d.Host.Stop(ctx, spec.Name)
	}
	return nil
}

// reconcile makes d's container match its record and stores the outcome. The
// caller holds d's lock. changed is true when the container was created or
// started.
func (s *Service) reconcile(ctx context.Context, d Database) (changed bool, err error) {
	defer func() { s.record(ctx, d, err) }()
	spec, _, err := specFor(d)
	if err != nil {
		return false, err
	}
	st, err := s.d.Host.Inspect(ctx, spec.Name)
	if err != nil {
		return false, err
	}
	if d.Desired == DesiredStopped {
		if st.Exists && st.Running {
			return false, s.d.Host.Stop(ctx, spec.Name)
		}
		return false, nil
	}
	if !st.Exists {
		if d.Initialized {
			ok, err := s.d.Host.VolumeExists(ctx, spec.Volume)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil // volume_missing: never recreate it silently
			}
		}
		return true, s.create(ctx, d, spec)
	}
	if !d.Initialized {
		if err := s.d.Store.SetInitialized(ctx, d.ID); err != nil {
			return false, err
		}
	}
	if st.Hash != spec.Labels[HashLabel] {
		return true, s.create(ctx, d, spec)
	}
	if !st.Running {
		if err := s.d.Host.Start(ctx, spec.Name); err != nil {
			return false, err
		}
		changed = true
	}
	if spec.Alias != "" && !containsString(st.Aliases, spec.Alias) {
		return changed, s.d.Host.Realias(ctx, spec.Name, spec.Alias)
	}
	return changed, nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// op runs fn on a fresh copy of Database id under its lock, after the
// Migration lock check.
func (s *Service) op(ctx context.Context, id int64, fn func(d Database) error) error {
	if s.locked(ctx) {
		return ErrLocked
	}
	unlock := s.lock(id)
	defer unlock()
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	return fn(d)
}

func (s *Service) Start(ctx context.Context, id int64) error {
	return s.op(ctx, id, func(d Database) error {
		if err := s.d.Store.SetDesired(ctx, id, DesiredRunning); err != nil {
			return err
		}
		d.Desired = DesiredRunning
		_, err := s.reconcile(ctx, d)
		return err
	})
}

func (s *Service) Stop(ctx context.Context, id int64) error {
	return s.op(ctx, id, func(d Database) error {
		if err := s.d.Store.SetDesired(ctx, id, DesiredStopped); err != nil {
			return err
		}
		d.Desired = DesiredStopped
		_, err := s.reconcile(ctx, d)
		return err
	})
}

func (s *Service) Restart(ctx context.Context, id int64) error {
	return s.op(ctx, id, func(d Database) (err error) {
		if err := s.d.Store.SetDesired(ctx, id, DesiredRunning); err != nil {
			return err
		}
		d.Desired = DesiredRunning
		changed, err := s.reconcile(ctx, d)
		if err != nil || changed {
			return err
		}
		st, err := s.d.Host.Inspect(ctx, ContainerName(id))
		if err != nil || !st.Exists {
			s.record(ctx, d, err)
			return err
		}
		err = s.d.Host.Restart(ctx, ContainerName(id))
		s.record(ctx, d, err)
		return err
	})
}

// Rename changes the display name and the readable alias. The container is
// not restarted.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	return s.op(ctx, id, func(d Database) error {
		sl := slug.Slugify(name)
		if free, err := s.slugFree(ctx, sl, id); err != nil {
			return err
		} else if !free {
			return ErrDuplicateName
		}
		if err := s.d.Store.Rename(ctx, id, name, sl); err != nil {
			return err
		}
		st, err := s.d.Host.Inspect(ctx, ContainerName(id))
		if err != nil {
			return err
		}
		if !st.Exists {
			return nil
		}
		return s.d.Host.Realias(ctx, ContainerName(id), Alias(sl))
	})
}

// UpdateImage pulls the Database's tag again and recreates the container on
// the same volume when the image changed.
func (s *Service) UpdateImage(ctx context.Context, id int64) error {
	return s.op(ctx, id, func(d Database) (err error) {
		defer func() { s.record(ctx, d, err) }()
		spec, _, err := specFor(d)
		if err != nil {
			return err
		}
		newID, err := s.d.Host.PullImage(ctx, spec.Image)
		if err != nil {
			return err
		}
		st, err := s.d.Host.Inspect(ctx, spec.Name)
		if err != nil {
			return err
		}
		if st.Exists && st.ImageID != newID {
			return s.create(ctx, d, spec)
		}
		return nil
	})
}

// Repair clears the last error and reconciles.
func (s *Service) Repair(ctx context.Context, id int64) error {
	return s.op(ctx, id, func(d Database) error {
		if d.LastError != "" {
			if err := s.d.Store.SetLastError(ctx, id, ""); err != nil {
				return err
			}
			d.LastError = ""
		}
		_, err := s.reconcile(ctx, d)
		return err
	})
}

// Delete removes the container, the volume and the record. confirm must equal
// the Database's name.
func (s *Service) Delete(ctx context.Context, id int64, confirm string) error {
	return s.op(ctx, id, func(d Database) error {
		if confirm != d.Name {
			return ErrConfirm
		}
		if err := s.d.Host.Remove(ctx, ContainerName(id)); err != nil {
			return err
		}
		if err := s.d.Host.RemoveVolume(ctx, VolumeName(id)); err != nil {
			return err
		}
		return s.d.Store.Delete(ctx, id)
	})
}

func (s *Service) Logs(ctx context.Context, id int64, tail int) (string, error) {
	if _, err := s.d.Store.Get(ctx, id); err != nil {
		return "", err
	}
	return s.d.Host.Logs(ctx, ContainerName(id), tail)
}

// Get returns one Database with its status.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, d), nil
}

func (s *Service) List(ctx context.Context) ([]View, error) {
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(all))
	for _, d := range all {
		out = append(out, s.view(ctx, d))
	}
	return out, nil
}

func (s *Service) view(ctx context.Context, d Database) View {
	v := View{Database: d, Warnings: []string{}}
	if Alias(d.Slug) == "" {
		v.Warnings = append(v.Warnings, "No readable hostname: the name has no usable letters or digits, or is too long. Use the stable host.")
	}
	spec, _, err := specFor(d)
	if err != nil {
		v.Status = StatusError
		return v
	}
	if s.isInflight(d.ID) {
		v.Status = StatusCreating
		return v
	}
	st, err := s.d.Host.Inspect(ctx, spec.Name)
	if err != nil {
		v.Status = StatusError
		v.Warnings = append(v.Warnings, err.Error())
		return v
	}
	volumeGone := false
	if d.Initialized && !st.Exists {
		ok, err := s.d.Host.VolumeExists(ctx, spec.Volume)
		volumeGone = err == nil && !ok
	}
	switch {
	case d.LastError != "":
		v.Status = StatusError
	case volumeGone:
		v.Status = StatusVolumeMissing
	case d.Desired == DesiredStopped:
		v.Status = StatusStopped
	case !st.Exists || st.Hash != spec.Labels[HashLabel]:
		v.Status = StatusDrift
	case !st.Running:
		v.Status = StatusStopped
	case st.Health == "starting":
		v.Status = StatusStarting
	case st.Health == "unhealthy":
		v.Status = StatusUnhealthy
	default:
		v.Status = StatusRunning
	}
	return v
}

// ReconcileAll reconciles every Database. A Migration lock makes it a no-op.
// Errors are stored per Database; the first is returned.
func (s *Service) ReconcileAll(ctx context.Context) error {
	if s.locked(ctx) {
		return nil
	}
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, d := range all {
		err := func() error {
			unlock := s.lock(d.ID)
			defer unlock()
			fresh, err := s.d.Store.Get(ctx, d.ID)
			if err != nil {
				return err
			}
			_, err = s.reconcile(ctx, fresh)
			return err
		}()
		if err != nil && first == nil {
			first = fmt.Errorf("%s: %w", d.Name, err)
		}
	}
	return first
}

// StopAll stops every Database's container and leaves Desired as it is. The
// Migration calls it while it holds the lock, so it ignores the lock.
func (s *Service) StopAll(ctx context.Context) error {
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range all {
		func() {
			unlock := s.lock(d.ID)
			defer unlock()
			st, err := s.d.Host.Inspect(ctx, ContainerName(d.ID))
			if err == nil && st.Exists && st.Running {
				err = s.d.Host.Stop(ctx, ContainerName(d.ID))
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
			}
		}()
	}
	return errors.Join(errs...)
}

// StartWanted reconciles each Database whose Desired is running. The Migration
// calls it while it holds the lock, so it ignores the lock.
func (s *Service) StartWanted(ctx context.Context) error {
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range all {
		if d.Desired != DesiredRunning {
			continue
		}
		func() {
			unlock := s.lock(d.ID)
			defer unlock()
			if _, err := s.reconcile(ctx, d); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
			}
		}()
	}
	return errors.Join(errs...)
}

// Volumes names the volumes of Databases that have started once: the ones a
// Migration must copy.
func (s *Service) Volumes(ctx context.Context) ([]string, error) {
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, d := range all {
		if d.Initialized {
			out = append(out, VolumeName(d.ID))
		}
	}
	return out, nil
}
