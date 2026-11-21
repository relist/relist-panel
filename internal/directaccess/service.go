package directaccess

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

var (
	ErrInvalid        = errors.New("invalid request")
	ErrUnsupported    = errors.New("not supported yet")
	ErrOpen           = errors.New("this Database already has a Direct access")
	ErrTargetNotFound = errors.New("target not found")
	ErrTargetStopped  = errors.New("start the Database first")
	ErrLocked         = errors.New("a server Migration is running; Direct access cannot change until it ends")
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Durations an Admin may pick, in minutes; the longest is the most an expiry
// can be from now.
var allowedMinutes = []int{15, 60, 240, 480}

const (
	minHostPort    = 1024
	maxHostPort    = 65535
	reconcileEvery = time.Minute
)

// Endpoint is where a target can be reached on its Docker network.
type Endpoint struct {
	Name          string // display name, for audit
	Network, Host string
	Port          int
	Running       bool
}

// Targets resolves a target to an Endpoint. Each target kind is implemented
// by the package that owns it, so directaccess never imports them. An unknown
// or deleted target gives ErrTargetNotFound.
type Targets interface {
	Resolve(ctx context.Context, kind string, id int64) (Endpoint, error)
}

type Deps struct {
	Store   *Store
	Host    Host
	Targets Targets
	// Locked reports the server Migration lock (nil = never locked).
	Locked func(ctx context.Context) bool
	Now    func() time.Time
}

type OpenInput struct {
	TargetKind    string
	TargetID      int64
	Channel       string
	AllInterfaces bool
	HostPort      int // 0 = Docker picks
	Minutes       int
	OpenedBy      int64
}

// Service opens, extends and closes Direct accesses. One mutex serialises
// every change, including Reconcile; it never takes a Database's job slot.
type Service struct {
	d  Deps
	mu sync.Mutex
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

func validMinutes(m int) error {
	for _, ok := range allowedMinutes {
		if m == ok {
			return nil
		}
	}
	return fmt.Errorf("%w: minutes must be 15, 60, 240 or 480", ErrInvalid)
}

func (s *Service) expired(a Access) bool {
	return a.ExpiresAt != nil && !s.d.Now().Before(*a.ExpiresAt)
}

// cleanupCtx outlives a cancelled request: a half-done open must still be
// undone.
func cleanupCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

// end removes the proxy and then the row; the row is what says it is open.
func (s *Service) end(ctx context.Context, id int64) error {
	if err := s.d.Host.RemoveProxy(ctx, id); err != nil {
		return err
	}
	return s.d.Store.Delete(ctx, id)
}

func (s *Service) Open(ctx context.Context, in OpenInput) (Access, error) {
	if in.TargetKind != KindDatabase {
		return Access{}, fmt.Errorf("target %q: %w", in.TargetKind, ErrUnsupported)
	}
	if in.Channel != ChannelHostPort {
		return Access{}, fmt.Errorf("channel %q: %w", in.Channel, ErrUnsupported)
	}
	if err := validMinutes(in.Minutes); err != nil {
		return Access{}, err
	}
	if in.HostPort != 0 && (in.HostPort < minHostPort || in.HostPort > maxHostPort) {
		return Access{}, fmt.Errorf("%w: port must be 0 or between %d and %d", ErrInvalid, minHostPort, maxHostPort)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Locked != nil && s.d.Locked(ctx) {
		return Access{}, ErrLocked
	}
	ep, err := s.d.Targets.Resolve(ctx, in.TargetKind, in.TargetID)
	if err != nil {
		return Access{}, err
	}
	if !ep.Running {
		return Access{}, ErrTargetStopped
	}
	existing, err := s.d.Store.ForTarget(ctx, in.TargetKind, in.TargetID)
	if err != nil {
		return Access{}, err
	}
	for _, a := range existing {
		if !s.expired(a) {
			return Access{}, ErrOpen
		}
		// A row past its expiry only waits for reconcile; clear it now.
		if err := s.end(ctx, a.ID); err != nil {
			return Access{}, err
		}
	}

	now := s.d.Now()
	deadline := now.Add(time.Duration(in.Minutes) * time.Minute)
	bind := "127.0.0.1"
	if in.AllInterfaces {
		bind = "0.0.0.0"
	}
	// The row comes first: its ID names the container.
	id, err := s.d.Store.Insert(ctx, Access{
		Target:    Target{Kind: in.TargetKind, ID: in.TargetID, Port: ep.Port},
		Channel:   in.Channel,
		HostPort:  HostPort{Bind: bind, HostPort: in.HostPort},
		ExpiresAt: &deadline,
		OpenedBy:  in.OpenedBy,
		OpenedAt:  now,
	})
	if err != nil {
		return Access{}, err
	}
	undo := func(err error) (Access, error) {
		cctx, cancel := cleanupCtx(ctx)
		defer cancel()
		_ = s.d.Host.RemoveProxy(cctx, id)
		_ = s.d.Store.Delete(cctx, id)
		return Access{}, err
	}
	port, err := s.d.Host.StartProxy(ctx, ProxySpec{
		AccessID: id, TargetLabel: in.TargetKind + ":" + itoa(in.TargetID),
		Network: ep.Network, Upstream: ep.Host + ":" + strconv.Itoa(ep.Port), ListenPort: ep.Port,
		BindIP: bind, HostPort: in.HostPort, Deadline: deadline,
	})
	if err != nil {
		return undo(err)
	}
	if err := s.d.Store.SetHostPort(ctx, id, port); err != nil {
		return undo(err)
	}
	return s.d.Store.Get(ctx, id)
}

// Extend sets the expiry to now + minutes, which is at most 8 hours from now.
// It never restarts the proxy. A Direct access that ended between the page
// load and the click is ErrNotFound, and its row is gone.
func (s *Service) Extend(ctx context.Context, id int64, minutes int) (Access, error) {
	if err := validMinutes(minutes); err != nil {
		return Access{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return Access{}, err
	}
	if s.expired(a) {
		if err := s.end(ctx, id); err != nil {
			return Access{}, err
		}
		return Access{}, ErrNotFound
	}
	deadline := s.d.Now().Add(time.Duration(minutes) * time.Minute)
	if err := s.d.Host.Extend(ctx, id, deadline); err != nil {
		if errors.Is(err, ErrGone) {
			if derr := s.end(ctx, id); derr != nil {
				return Access{}, derr
			}
			return Access{}, ErrNotFound
		}
		return Access{}, err
	}
	if err := s.d.Store.SetExpiry(ctx, id, deadline); err != nil {
		return Access{}, err
	}
	return s.d.Store.Get(ctx, id)
}

func (s *Service) Close(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.d.Store.Get(ctx, id); err != nil {
		return err
	}
	return s.end(ctx, id)
}

// Get returns one Direct access, expired or not.
func (s *Service) Get(ctx context.Context, id int64) (Access, error) {
	return s.d.Store.Get(ctx, id)
}

// ForTarget lists a target's open Direct accesses; one past its expiry is
// hidden even before reconcile removes it.
func (s *Service) ForTarget(ctx context.Context, kind string, id int64) ([]Access, error) {
	all, err := s.d.Store.ForTarget(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	out := make([]Access, 0, len(all))
	for _, a := range all {
		if !s.expired(a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// TargetName is the target's display name for audit, falling back to
// "<kind> <id>" when it no longer resolves.
func (s *Service) TargetName(ctx context.Context, kind string, id int64) string {
	if ep, err := s.d.Targets.Resolve(ctx, kind, id); err == nil && ep.Name != "" {
		return ep.Name
	}
	return kind + " " + itoa(id)
}

// CloseTarget ends every Direct access of one target, expired or not. It is
// what deleting a Database does first. The first error is returned, but every
// access is tried.
func (s *Service) CloseTarget(ctx context.Context, kind string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.d.Store.ForTarget(ctx, kind, id)
	if err != nil {
		return err
	}
	var first error
	for _, a := range all {
		if err := s.end(ctx, a.ID); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Reconcile makes Docker match the table, both ways. The table is the truth:
// the expires label is never read, because Extend cannot change it.
//   - a container with no row is removed;
//   - a row past its expiry loses its container and the row;
//   - a row with no running container loses the row;
//   - an access whose target no longer exists loses both.
func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proxies, err := s.d.Host.ListProxies(ctx)
	if err != nil {
		return err
	}
	rows, err := s.d.Store.List(ctx)
	if err != nil {
		return err
	}
	byID := map[int64]Access{}
	for _, a := range rows {
		byID[a.ID] = a
	}
	running := map[int64]bool{}
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, p := range proxies {
		a, ok := byID[p.AccessID]
		if !ok || s.expired(a) || !p.Running || s.targetGone(ctx, a) {
			note(s.d.Host.RemoveProxy(ctx, p.AccessID))
			continue
		}
		running[p.AccessID] = true
	}
	for _, a := range rows {
		if running[a.ID] {
			continue
		}
		note(s.d.Store.Delete(ctx, a.ID))
	}
	return first
}

// targetGone reports a target that no longer exists. An Open can slip in
// between a Database delete's CloseTarget and the container's removal; this
// ends such an access at the next reconcile rather than at its expiry. Any
// other resolve error keeps the access.
func (s *Service) targetGone(ctx context.Context, a Access) bool {
	_, err := s.d.Targets.Resolve(ctx, a.Target.Kind, a.Target.ID)
	return errors.Is(err, ErrTargetNotFound)
}

// Run reconciles at start and every minute until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(reconcileEvery)
	defer t.Stop()
	for {
		_ = s.Reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
