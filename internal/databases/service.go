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
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"relist-panel/internal/backupdest"
	"relist-panel/internal/directaccess"
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
	// StatusDeleted is a tombstone: deleted, with its Backups kept.
	StatusDeleted = "deleted"
)

var (
	ErrInvalidName   = errors.New("name must be 1 to 63 characters")
	ErrConfirm       = errors.New("type the Database's name to confirm")
	ErrLocked        = errors.New("a server Migration is running; Databases cannot change until it ends")
	ErrInvalidLimits = errors.New("memory and CPU limits cannot be negative")
)

// Deps wires the Service. Locked reports the server Migration lock (nil =
// never locked).
type Deps struct {
	Store  *Store
	Host   Host
	Locked func(ctx context.Context) bool
	// UpdateLocked reports the Panel update lock (nil = never locked). While
	// it is held, no Backup or restore starts.
	UpdateLocked func() bool
	// DataDir is the panel's data dir; Backups live under .databases/<id>/.
	DataDir string
	Now     func() time.Time
	// Destinations opens Backup destinations for uploads (nil = scheduled
	// Backups cannot be copied anywhere).
	Destinations backupdest.Opener
	// Querier runs the Query panel's statements (nil = the real drivers).
	Querier Querier
	// DirectAccess closes a Database's Direct accesses before it is deleted
	// (nil = it has none).
	DirectAccess DirectAccessCloser
	// Admin runs the Databases and Users tabs' statements and the superuser
	// reset as the superuser (nil = the real drivers).
	Admin Admin
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
	// SettingsOutdated: this panel's driver would create the container with
	// other settings than the applied ones. ApplySettings or UpdateImage
	// applies them; nothing else does.
	SettingsOutdated bool
	// Job is the job running on the Database: "", "backup", "restore" or
	// "update_image". Deleted is a tombstone.
	Job     string
	Deleted bool
	// PreviousVolumes is the previous version data a version change left
	// behind and nobody has deleted yet.
	PreviousVolumes []string
}

// Service runs the panel's Databases. The database is the source of truth;
// Reconcile makes the host match it (ADR-0039).
type Service struct {
	d Deps

	mu       sync.Mutex
	locks    map[int64]*sync.Mutex
	inflight map[int64]bool
	wg       sync.WaitGroup
	// jobs is the one running job per Database ("backup", "restore",
	// "update_image"); jobsChanged is closed and replaced whenever one ends.
	jobs        map[int64]string
	jobsChanged chan struct{}
	// uploadStall is how long an upload may send nothing before it is cut
	// off; remoteTimeout bounds one delete in a Backup destination. A bucket
	// that stops answering would otherwise hold the job slot, and the
	// host-wide scheduler with it, for ever.
	uploadStall, remoteTimeout time.Duration
	// pendingRetry is the wait between two tries to settle a pending
	// superuser password, pendingTries how many tries there are.
	pendingRetry time.Duration
	pendingTries int
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Querier == nil {
		d.Querier = engineQuerier{}
	}
	if d.Admin == nil {
		d.Admin = engineAdmin{}
	}
	return &Service{d: d, locks: map[int64]*sync.Mutex{}, inflight: map[int64]bool{}, jobs: map[int64]string{}, jobsChanged: make(chan struct{}),
		uploadStall: 5 * time.Minute, remoteTimeout: time.Minute,
		pendingRetry: 5 * time.Second, pendingTries: 24}
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
		return Database{}, ErrInvalidLimits
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

// specFor builds the container spec the running panel's driver would choose
// for d today. Reconcile does not use it directly: it uses appliedSpec, so a
// Panel update with new driver defaults never recreates a Database.
func specFor(d Database) (ContainerSpec, Engine, error) {
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		return ContainerSpec{}, nil, err
	}
	spec := ContainerSpec{
		Name: ContainerName(d.ID), Volume: volumeOf(d),
		Image: eng.Image(d.Version), DataPath: eng.DataPath(d.Version),
		Env: eng.Env(d.Password), Cmd: eng.Cmd(), Healthcheck: eng.Healthcheck(),
		Alias:       Alias(d.Slug),
		MemoryBytes: d.MemoryMB * 1024 * 1024,
		NanoCPUs:    int64(d.CPUs * 1e9),
	}
	spec.Labels = map[string]string{IDLabel: fmt.Sprint(d.ID), HashLabel: hashSpec(spec)}
	return spec, eng, nil
}

// hashSpec is the relist.database.hash label: everything that needs a new
// container to change. Not the names, which derive from the ID, nor the
// alias, which a rename swaps without a restart.
func hashSpec(spec ContainerSpec) string {
	raw, _ := json.Marshal(struct {
		Image, DataPath       string
		Env, Cmd, Healthcheck []string
		MemoryBytes, NanoCPUs int64
	}{spec.Image, spec.DataPath, spec.Env, spec.Cmd, spec.Healthcheck, spec.MemoryBytes, spec.NanoCPUs})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// volumeOf is d's stored volume; a record from before the column has none.
func volumeOf(d Database) string {
	if d.Volume == "" {
		return VolumeName(d.ID)
	}
	return d.Volume
}

// appliedSpec is the spec d's container was last created with, with the
// names and alias of d as it is now. Before the first reconcile stored one,
// it is today's specFor.
func appliedSpec(d Database) (ContainerSpec, error) {
	if d.AppliedSpec == "" {
		spec, _, err := specFor(d)
		return spec, err
	}
	var spec ContainerSpec
	if err := json.Unmarshal([]byte(d.AppliedSpec), &spec); err != nil {
		return ContainerSpec{}, fmt.Errorf("read the applied settings: %w", err)
	}
	spec.Name, spec.Volume, spec.Alias = ContainerName(d.ID), volumeOf(d), Alias(d.Slug)
	spec.Labels = map[string]string{IDLabel: fmt.Sprint(d.ID), HashLabel: hashSpec(spec)}
	return spec, nil
}

// saveApplied records spec as the settings d's container is created with.
func (s *Service) saveApplied(ctx context.Context, d *Database, spec ContainerSpec) error {
	raw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	if err := s.d.Store.SetAppliedSpec(ctx, d.ID, string(raw)); err != nil {
		return err
	}
	d.AppliedSpec = string(raw)
	return nil
}

// record stores the outcome of one operation on d.
func (s *Service) record(ctx context.Context, d Database, err error) {
	ctx = context.WithoutCancel(ctx)
	switch {
	case err != nil && !errors.Is(err, ErrLocked) && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrNotRunning):
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
	spec, err := appliedSpec(d)
	if err != nil {
		return false, err
	}
	if d.AppliedSpec == "" {
		if err := s.saveApplied(ctx, &d, spec); err != nil {
			return false, err
		}
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
	if d.DeletedAt != nil {
		return ErrNotFound
	}
	return fn(d)
}

// opIdle is op for the actions a running job must not interleave with: it
// answers ErrBusy while a Backup, restore or update runs. The check is under
// the Database's lock, so a Stop either finishes before a job takes its slot
// or sees it.
func (s *Service) opIdle(ctx context.Context, id int64, fn func(d Database) error) error {
	return s.op(ctx, id, func(d Database) error {
		if s.Jobs(id) != "" {
			return ErrBusy
		}
		return fn(d)
	})
}

func (s *Service) Start(ctx context.Context, id int64) error {
	err := s.opIdle(ctx, id, func(d Database) error {
		if err := s.d.Store.SetDesired(ctx, id, DesiredRunning); err != nil {
			return err
		}
		d.Desired = DesiredRunning
		_, err := s.reconcile(ctx, d)
		return err
	})
	// An unsettled superuser password is settled once the container is up;
	// that waits for it, so it runs after the lock is released.
	if err == nil {
		if d, gerr := s.d.Store.Get(ctx, id); gerr == nil && d.PendingPassword != "" {
			s.goResolve(context.WithoutCancel(ctx), id)
		}
	}
	return err
}

func (s *Service) Stop(ctx context.Context, id int64) error {
	return s.opIdle(ctx, id, func(d Database) error {
		if err := s.d.Store.SetDesired(ctx, id, DesiredStopped); err != nil {
			return err
		}
		d.Desired = DesiredStopped
		_, err := s.reconcile(ctx, d)
		return err
	})
}

func (s *Service) Restart(ctx context.Context, id int64) error {
	return s.opIdle(ctx, id, func(d Database) (err error) {
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

// UpdateImage pulls the Database's tag again, applies this panel's settings,
// and recreates the container on the same volume when either changed. A
// stopped Database is not started: its container is removed, and the next
// Start creates the new one.
//
// With backupFirst it is a job: it checks the Database is running, answers at
// once, and in the background takes a pre_update Backup, then updates. A
// failed Backup stops it before the pull.
func (s *Service) UpdateImage(ctx context.Context, id int64, backupFirst bool) error {
	if !backupFirst {
		return s.opIdle(ctx, id, s.updateImage(ctx))
	}
	d, dp, err := s.acquire(ctx, id, JobKindUpdateImage)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		ctx := context.WithoutCancel(ctx)
		if ok, firstErr := s.backupAll(ctx, d, dp, TriggerPreUpdate); !ok {
			_ = s.d.Store.SetLastError(ctx, id, "the pre-update backup failed, so the image was not updated: "+firstErr)
			return
		}
		unlock := s.lock(id)
		defer unlock()
		fresh, err := s.d.Store.Get(ctx, id)
		if err != nil {
			return
		}
		_ = s.updateImage(ctx)(fresh)
	}()
	return nil
}

func (s *Service) updateImage(ctx context.Context) func(d Database) error {
	return func(d Database) (err error) {
		defer func() { s.record(ctx, d, err) }()
		spec, _, err := specFor(d)
		if err != nil {
			return err
		}
		if err := s.saveApplied(ctx, &d, spec); err != nil {
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
		if !st.Exists || (st.ImageID == newID && st.Hash == spec.Labels[HashLabel]) {
			return nil
		}
		if d.Desired == DesiredStopped {
			if err := s.d.Host.Stop(ctx, spec.Name); err != nil {
				return err
			}
			return s.d.Host.Remove(ctx, spec.Name)
		}
		return s.create(ctx, d, spec)
	}
}

// ApplySettings makes this panel's driver settings the applied ones and
// recreates the container with them. A stopped Database gets them on its
// next Start.
func (s *Service) ApplySettings(ctx context.Context, id int64) error {
	return s.opIdle(ctx, id, func(d Database) error {
		spec, _, err := specFor(d)
		if err != nil {
			return err
		}
		if err := s.saveApplied(ctx, &d, spec); err != nil {
			return err
		}
		_, err = s.reconcile(ctx, d)
		return err
	})
}

// Repair clears the last error and reconciles.
func (s *Service) Repair(ctx context.Context, id int64) error {
	return s.opIdle(ctx, id, func(d Database) error {
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

// Delete removes the container and the volume. confirm must equal the
// Database's name. With deleteBackups, or when it has no Backups, the record
// and its files go too; otherwise it stays as a tombstone that shows only its
// Backups.
func (s *Service) Delete(ctx context.Context, id int64, confirm string, deleteBackups bool) error {
	return s.opIdle(ctx, id, func(d Database) error {
		if confirm != d.Name {
			return ErrConfirm
		}
		// The proxy goes before the container it points at.
		if s.d.DirectAccess != nil {
			if err := s.d.DirectAccess.CloseTarget(ctx, directaccess.KindDatabase, id); err != nil {
				return err
			}
		}
		if deleteBackups {
			// Remote copies first: if one stays, nothing else is deleted.
			if err := s.deleteLiveCopies(ctx, id, nil); err != nil {
				return err
			}
		}
		if err := s.d.Host.Remove(ctx, ContainerName(id)); err != nil {
			return err
		}
		prev, err := s.d.Store.PreviousVolumes(ctx)
		if err != nil {
			return err
		}
		for _, v := range append([]string{volumeOf(d)}, prev[id]...) {
			if err := s.d.Host.RemoveVolume(ctx, v); err != nil {
				return err
			}
		}
		if !deleteBackups {
			kept, err := s.d.Store.ListBackups(ctx, id)
			if err != nil {
				return err
			}
			if len(kept) > 0 {
				// A tombstone has no schedule.
				if err := s.d.Store.DeleteSchedule(ctx, id); err != nil {
					return err
				}
				return s.d.Store.Tombstone(ctx, id)
			}
		}
		return s.removeRecord(ctx, id)
	})
}

// RemoveFiles removes everything the panel keeps on disk for Database id.
func (s *Service) RemoveFiles(id int64) error {
	if s.d.DataDir == "" {
		return nil
	}
	return os.RemoveAll(s.dbDir(id))
}

// removeRecord deletes a Database's row and its files.
func (s *Service) removeRecord(ctx context.Context, id int64) error {
	if s.d.DataDir != "" {
		if err := os.RemoveAll(s.dbDir(id)); err != nil {
			return err
		}
	}
	return s.d.Store.Delete(ctx, id)
}

func (s *Service) Logs(ctx context.Context, id int64, tail int) (string, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if d.DeletedAt != nil {
		return "", ErrNotFound
	}
	return s.d.Host.Logs(ctx, ContainerName(id), tail)
}

// Get returns one Database with its status.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	v := s.view(ctx, d)
	if !v.Deleted {
		prev, err := s.d.Store.PreviousVolumes(ctx)
		if err != nil {
			return View{}, err
		}
		v.PreviousVolumes = prev[id]
	}
	return v, nil
}

func (s *Service) List(ctx context.Context) ([]View, error) {
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	prev, err := s.d.Store.PreviousVolumes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(all))
	for _, d := range all {
		v := s.view(ctx, d)
		if !v.Deleted {
			v.PreviousVolumes = prev[d.ID]
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) view(ctx context.Context, d Database) View {
	v := View{Database: d, Warnings: []string{}, Job: s.Jobs(d.ID)}
	if d.DeletedAt != nil {
		v.Status, v.Deleted = StatusDeleted, true
		return v
	}
	if Alias(d.Slug) == "" {
		v.Warnings = append(v.Warnings, "No readable hostname: the name has no usable letters or digits, or is too long. Use the stable host.")
	}
	if d.PendingPassword != "" && v.Job != JobKindResetPassword {
		v.Warnings = append(v.Warnings, pendingWarning)
	}
	spec, err := appliedSpec(d)
	if err != nil {
		v.Status = StatusError
		return v
	}
	if current, _, err := specFor(d); err == nil {
		v.SettingsOutdated = current.Labels[HashLabel] != spec.Labels[HashLabel]
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
		if d.DeletedAt != nil {
			continue
		}
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
		if d.DeletedAt != nil {
			continue
		}
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
		if d.Desired != DesiredRunning || d.DeletedAt != nil {
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

// Volumes names the volumes of Databases that have started once, each
// followed by its previous version data: the ones a Migration must copy.
func (s *Service) Volumes(ctx context.Context) ([]string, error) {
	all, err := s.d.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	prev, err := s.d.Store.PreviousVolumes(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, d := range all {
		if d.Initialized && d.DeletedAt == nil {
			out = append(out, volumeOf(d))
			out = append(out, prev[d.ID]...)
		}
	}
	return out, nil
}
