package registry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidHostname = errors.New("hostname must be a DNS name such as registry.example.com, without a scheme or port")
	ErrInvalidTLS      = errors.New("tls must be letsencrypt or certificate")
	ErrNoHostname      = errors.New("set the Registry's hostname first")
	ErrStillEnabled    = errors.New("disable the Registry before deleting its data")
	ErrConfirm         = errors.New("type the Registry's hostname to confirm")
)

// ErrHostnameConflict is a hostname something else on this host serves.
type ErrHostnameConflict struct{ Message string }

func (e *ErrHostnameConflict) Error() string { return e.Message }

// The Registry's status, as the Settings tab shows it.
const (
	StatusOff      = "off"
	StatusRunning  = "running"
	StatusReadOnly = "read-only"
	StatusStopped  = "stopped"
	StatusDrift    = "drift"
	StatusError    = "error"
)

// Deps wires the Service. DomainHosts maps each Domain host to its Project's
// name; ComposesUsing names the Composes whose last deploy used an image on
// host.
type Deps struct {
	Store         *Store
	Host          Host
	Files         Files
	PanelDomain   string
	DomainHosts   func(ctx context.Context) (map[string]string, error)
	ComposesUsing func(ctx context.Context, host string) ([]string, error)
	Image         string // "" is DefaultImage
	Now           func() time.Time
}

// Service runs the Panel registry. The database is the source of truth;
// Reconcile makes the host match it (ADR-0033).
type Service struct {
	d  Deps
	mu sync.Mutex // one change to the container at a time
	infoCache

	// disk caches the volume's usage for a minute: Docker sizes every volume
	// on the host to answer it. Whatever changes the usage forgets it.
	diskMu    sync.Mutex
	diskBytes int64
	diskAt    time.Time
}

// diskTTL is how long a volume usage reading is shown before it is read again.
const diskTTL = time.Minute

// cachedVolumeBytes is the Registry volume's usage, read at most once a
// minute. A failed read shows the last one and waits the same minute before
// asking Docker again. The lock is held across the read, so loads arriving
// meanwhile share it instead of each asking Docker.
func (s *Service) cachedVolumeBytes(ctx context.Context) int64 {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	if !s.diskAt.IsZero() && s.d.Now().Sub(s.diskAt) < diskTTL {
		return s.diskBytes
	}
	if n, err := s.d.Host.VolumeBytes(ctx); err == nil {
		s.diskBytes = n
	}
	s.diskAt = s.d.Now()
	return s.diskBytes
}

// forgetDisk makes the next load read the volume's usage again.
func (s *Service) forgetDisk() {
	s.diskMu.Lock()
	s.diskAt = time.Time{}
	s.diskMu.Unlock()
}

func New(d Deps) *Service {
	if d.Image == "" {
		d.Image = DefaultImage
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

var hostnamePattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Save validates and stores hostname and TLS. An enabled Registry is
// recreated to serve the new values.
func (s *Service) Save(ctx context.Context, in Settings) error {
	host := strings.ToLower(strings.TrimSpace(in.Hostname))
	if len(host) > 253 || !hostnamePattern.MatchString(host) {
		return ErrInvalidHostname
	}
	if in.TLS != TLSLetsEncrypt && in.TLS != TLSCertificate {
		return ErrInvalidTLS
	}
	if s.d.PanelDomain != "" && host == strings.ToLower(s.d.PanelDomain) {
		return &ErrHostnameConflict{Message: host + " is the panel's own domain"}
	}
	if s.d.DomainHosts != nil {
		hosts, err := s.d.DomainHosts(ctx)
		if err != nil {
			return err
		}
		if project, ok := hosts[host]; ok {
			return &ErrHostnameConflict{Message: host + " is a Domain of " + project}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	st.Hostname, st.TLS = host, in.TLS
	if err := s.d.Store.SaveSettings(ctx, st); err != nil {
		return err
	}
	if st.Enabled {
		_, err = s.reconcile(ctx, st)
	}
	return err
}

// Enable pulls the image, writes the files and starts the container. A first
// Enable makes the hidden system login.
func (s *Service) Enable(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.forgetDisk()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if st.Hostname == "" {
		return ErrNoHostname
	}
	if st.SystemSecret == "" {
		secret, hash, err := NewToken()
		if err != nil {
			return err
		}
		st.SystemSecret, st.SystemHash = secret, hash
	}
	st.Enabled = true
	if err := s.start(ctx, st); err != nil {
		st.Enabled = false
		st.LastError = err.Error()
		if serr := s.d.Store.SaveSettings(ctx, st); serr != nil {
			return errors.Join(err, serr)
		}
		return err
	}
	st.LastError = ""
	if err := s.d.Store.SaveSettings(ctx, st); err != nil {
		return err
	}
	// The cleanup schedule starts from now: a run "missed" while the
	// Registry was off must not fire the moment it comes on.
	return s.d.Store.SetLastRunAt(ctx, s.d.Now())
}

// start pulls, writes the files and (re)creates the container for st.
func (s *Service) start(ctx context.Context, st Settings) error {
	if err := s.d.Host.EnsureImage(ctx, s.d.Image); err != nil {
		return fmt.Errorf("could not pull %s: %w", s.d.Image, err)
	}
	if err := s.writeFiles(ctx, st); err != nil {
		return err
	}
	if err := s.d.Host.Create(ctx, s.spec(st)); err != nil {
		return fmt.Errorf("could not start the Registry: %w", err)
	}
	return nil
}

// writeFiles writes config.yml, keeping a read-only flag a cleanup set, and
// htpasswd from the stored logins.
func (s *Service) writeFiles(ctx context.Context, st Settings) error {
	creds, err := s.d.Store.Credentials(ctx)
	if err != nil {
		return err
	}
	readOnly, err := s.d.Files.ReadOnly()
	if err != nil {
		return err
	}
	return s.d.Files.Write(RenderConfig(readOnly), RenderHtpasswd(st.SystemHash, creds))
}

func (s *Service) spec(st Settings) ContainerSpec {
	return ContainerSpec{Image: s.d.Image, Hostname: st.Hostname, TLS: st.TLS, Hash: s.hash(st), FilesDir: s.d.Files.Dir}
}

// hash names what the container is made from; a change means recreate. The
// read-only flag is left out: it changes by restart, not recreate.
func (s *Service) hash(st Settings) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n", s.d.Image, st.Hostname, st.TLS)
	h.Write(RenderConfig(false))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Disable removes the container, and with it the route; the volume and files
// stay. It returns the Composes whose next deploy will fail to pull.
func (s *Service) Disable(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.forgetDisk()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	using, err := s.composesUsing(ctx, st.Hostname)
	if err != nil {
		return nil, err
	}
	if err := s.d.Host.Remove(ctx); err != nil {
		return nil, err
	}
	st.Enabled, st.LastError = false, ""
	return using, s.d.Store.SaveSettings(ctx, st)
}

// ComposesUsing names the Composes whose last deploy used an image on the
// Registry's hostname.
func (s *Service) ComposesUsing(ctx context.Context) ([]string, error) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return s.composesUsing(ctx, st.Hostname)
}

func (s *Service) composesUsing(ctx context.Context, host string) ([]string, error) {
	if host == "" || s.d.ComposesUsing == nil {
		return nil, nil
	}
	return s.d.ComposesUsing(ctx, host)
}

// Reconcile makes the container match the stored settings: create it when
// missing or made from other settings, start it when stopped. drift is true
// when it changed something. A disabled Registry is left alone.
func (s *Service) Reconcile(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Store.Settings(ctx)
	if err != nil || !st.Enabled {
		return false, err
	}
	return s.reconcile(ctx, st)
}

func (s *Service) reconcile(ctx context.Context, st Settings) (bool, error) {
	if err := s.writeFiles(ctx, st); err != nil {
		return false, err
	}
	state, err := s.d.Host.Inspect(ctx)
	if err != nil {
		return false, err
	}
	switch {
	case !state.Exists || state.Hash != s.hash(st) || state.Image != s.d.Image:
		if err := s.start(ctx, st); err != nil {
			_ = s.d.Store.SetLastError(ctx, err.Error())
			return true, err
		}
	case !state.Running:
		if err := s.d.Host.Restart(ctx); err != nil {
			_ = s.d.Store.SetLastError(ctx, err.Error())
			return true, err
		}
	default:
		// Nothing to repair; an error left from an earlier attempt is stale.
		if st.LastError != "" {
			return false, s.d.Store.SetLastError(ctx, "")
		}
		return false, nil
	}
	return true, s.d.Store.SetLastError(ctx, "")
}

// Status is what the Settings tab shows: off, error, drift, stopped,
// read-only or running.
func (s *Service) Status(ctx context.Context) (string, error) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return "", err
	}
	if !st.Enabled {
		if st.LastError != "" {
			return StatusError, nil
		}
		return StatusOff, nil
	}
	if st.LastError != "" {
		return StatusError, nil
	}
	state, err := s.d.Host.Inspect(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case !state.Exists || state.Hash != s.hash(st) || state.Image != s.d.Image:
		return StatusDrift, nil
	case !state.Running:
		return StatusStopped, nil
	}
	if ro, err := s.d.Files.ReadOnly(); err != nil {
		return "", err
	} else if ro {
		return StatusReadOnly, nil
	}
	return StatusRunning, nil
}

// DeleteData removes the registry volume of a disabled Registry once the
// operator typed its hostname.
func (s *Service) DeleteData(ctx context.Context, confirm string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.forgetDisk()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if st.Enabled {
		return ErrStillEnabled
	}
	if st.Hostname == "" || strings.TrimSpace(confirm) != st.Hostname {
		return ErrConfirm
	}
	return s.d.Host.RemoveVolume(ctx)
}

// Hostname is the Registry's hostname while it is enabled.
func (s *Service) Hostname(ctx context.Context) (string, bool) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil || !st.Enabled || st.Hostname == "" {
		return "", false
	}
	return st.Hostname, true
}

// NewToken is 32 random bytes, base64url without padding, and its bcrypt
// hash at the default cost.
func NewToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	h, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	return token, string(h), err
}
