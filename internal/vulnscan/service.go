package vulnscan

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrDisabled            = errors.New("Scanning is off")
	ErrInvalidScannerImage = errors.New("Scanner image must be repo[:tag]@sha256:<64 hex digits>")
)

type Deps struct {
	Store   *Store
	Scanner Scanner
	InUse   InUse
	Gate    DeployGate
	Now     func() time.Time
	Tick    time.Duration // how often a waiting worker re-checks the Deployment queue
}

// Service owns the scan queue, its worker and scheduler, and the
// operator's switches.
type Service struct {
	d    Deps
	wake chan struct{}
	// hubBase and hubClient reach the Docker Hub API for the tag list.
	hubBase   string
	hubClient *http.Client
	// mu serializes changes to the switches and the Scanner image.
	mu sync.Mutex
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Tick <= 0 {
		d.Tick = 2 * time.Second
	}
	return &Service{d: d, wake: make(chan struct{}, 1), hubBase: "https://hub.docker.com", hubClient: &http.Client{Timeout: 10 * time.Second}}
}

func logf(format string, args ...any) { log.Printf("vulnscan: "+format, args...) }

func (s *Service) wakeUp() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Enable pulls and checks the Scanner image, then turns scanning on and
// queues a DB update and a scan of every image in use. A failed pull leaves
// scanning off.
func (s *Service) Enable(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if st.Enabled {
		return nil
	}
	ref := st.EffectiveImage()
	if err := s.d.Scanner.EnsureImage(ctx, ref); err != nil {
		return err
	}
	if err := s.d.Store.SetActiveImage(ctx, ref); err != nil {
		return err
	}
	st.Enabled = true
	if err := s.d.Store.SaveSettings(ctx, st); err != nil {
		return err
	}
	if _, err := s.d.Store.Enqueue(ctx, KindDBUpdate, "", ReasonSetup, s.d.Now()); err != nil {
		return err
	}
	_, err = s.ScanAll(ctx, ReasonSetup)
	return err
}

// Disable turns scanning off and empties the queue. A running task finishes;
// results and the cache stay.
func (s *Service) Disable(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	st.Enabled = false
	if err := s.d.Store.SaveSettings(ctx, st); err != nil {
		return err
	}
	return s.d.Store.ClearQueue(ctx)
}

// SetScannerImage switches to ref ("" = the built-in default) once it has
// been pulled and checked. On failure the old image stays in use.
func (s *Service) SetScannerImage(ctx context.Context, ref string) error {
	if ref != "" && !ValidScannerRef(ref) {
		return ErrInvalidScannerImage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	next := Settings{ScannerImage: ref}.EffectiveImage()
	if next != st.ActiveImage {
		if err := s.d.Scanner.EnsureImage(ctx, next); err != nil {
			return err
		}
		if err := s.d.Store.SetActiveImage(ctx, next); err != nil {
			return err
		}
		if st.ActiveImage != "" {
			if err := s.d.Scanner.RemoveImage(ctx, st.ActiveImage); err != nil {
				logf("remove the old scanner image %s: %v", st.ActiveImage, err)
			}
		}
		if st.Enabled {
			s.enqueueScanner(ctx, next, ReasonSetup)
		}
	}
	st.ScannerImage = ref
	return s.d.Store.SaveSettings(ctx, st)
}

func (s *Service) enqueueScanner(ctx context.Context, ref string, reason Reason) {
	id, err := s.d.Scanner.ImageID(ctx, ref)
	if err != nil {
		logf("find the scanner image %s: %v", ref, err)
		return
	}
	if _, err := s.d.Store.Enqueue(ctx, KindImage, id, reason, s.d.Now()); err != nil {
		logf("queue the scanner image: %v", err)
		return
	}
	s.wakeUp()
}

// UsedImages is every image in use plus the Scanner's own.
func (s *Service) UsedImages(ctx context.Context) ([]UsedImage, error) {
	used, err := s.d.InUse.Images(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	ref := st.EffectiveImage()
	id, err := s.d.Scanner.ImageID(ctx, ref)
	if err != nil || id == "" {
		return used, nil // not pulled yet
	}
	scanner := ImageUser{Kind: UserScanner}
	for i := range used {
		if used[i].ImageID == id {
			used[i].Users = append(used[i].Users, scanner)
			return used, nil
		}
	}
	return append(used, UsedImage{ImageID: id, Refs: []string{ref}, Users: []ImageUser{scanner}}), nil
}

// dbMaxAge is how old the DB may get before a scan request refreshes it.
const dbMaxAge = 24 * time.Hour

// enabled reports ErrDisabled when scanning is off. When it is on, it
// queues a DB update ahead of the scans about to be queued if there is no
// DB, or the last good update is older than dbMaxAge. A failed update keeps
// the old time, so every request retries it until one succeeds.
func (s *Service) enabled(ctx context.Context) error {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if !st.Enabled {
		return ErrDisabled
	}
	s.queueDBIfOld(ctx, st)
	return nil
}

func (s *Service) queueDBIfOld(ctx context.Context, st Settings) {
	if st.DBVersion != "" && st.DBUpdatedAt != nil && s.d.Now().Sub(*st.DBUpdatedAt) <= dbMaxAge {
		return
	}
	if _, err := s.d.Store.Enqueue(ctx, KindDBUpdate, "", ReasonSetup, s.d.Now()); err != nil {
		logf("queue a DB update: %v", err)
	}
}

// UpdateDB queues a DB update whatever the DB's age. Trivy still skips the
// download when its cached DB is current.
func (s *Service) UpdateDB(ctx context.Context) error {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if !st.Enabled {
		return ErrDisabled
	}
	if _, err := s.d.Store.Enqueue(ctx, KindDBUpdate, "", ReasonManual, s.d.Now()); err != nil {
		return err
	}
	s.wakeUp()
	return nil
}

// ScanAll queues every image in use, the Scanner's included.
func (s *Service) ScanAll(ctx context.Context, reason Reason) (int, error) {
	if err := s.enabled(ctx); err != nil {
		return 0, err
	}
	used, err := s.UsedImages(ctx)
	if err != nil {
		return 0, err
	}
	for _, image := range used {
		if _, err := s.d.Store.Enqueue(ctx, KindImage, image.ImageID, reason, s.d.Now()); err != nil {
			return 0, err
		}
	}
	s.wakeUp()
	return len(used), nil
}

func (s *Service) ScanImage(ctx context.Context, imageID string, reason Reason) error {
	if err := s.enabled(ctx); err != nil {
		return err
	}
	if _, err := s.d.Store.Enqueue(ctx, KindImage, imageID, reason, s.d.Now()); err != nil {
		return err
	}
	s.wakeUp()
	return nil
}

// ScanCompose queues the images a Compose's Services run, after a
// successful Deployment, when scanning after deploy is on.
func (s *Service) ScanCompose(ctx context.Context, composeID int64) error {
	st, err := s.d.Store.Settings(ctx)
	if err != nil || !st.Enabled || !st.AfterDeploy {
		return err
	}
	s.queueDBIfOld(ctx, st)
	used, err := s.d.InUse.Images(ctx)
	if err != nil {
		return err
	}
	for _, image := range used {
		for _, u := range image.Users {
			if u.Kind == UserService && u.ComposeID == composeID {
				if _, err := s.d.Store.Enqueue(ctx, KindImage, image.ImageID, ReasonDeploy, s.d.Now()); err != nil {
					return err
				}
				break
			}
		}
	}
	s.wakeUp()
	return nil
}

// ScannerImage is the Scanner image reference in effect.
func (s *Service) ScannerImage(ctx context.Context) (string, error) {
	st, err := s.d.Store.Settings(ctx)
	return st.EffectiveImage(), err
}

// ScannerRefs spells ref the ways Docker lists it: repo:tag and repo@digest,
// so a prune can recognise the Scanner image.
func ScannerRefs(ref string) []string {
	name, digest, _ := strings.Cut(ref, "@")
	refs := []string{}
	repo := name
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		repo = name[:i]
		refs = append(refs, name)
	}
	if digest != "" {
		refs = append(refs, repo+"@"+digest)
	}
	return refs
}
