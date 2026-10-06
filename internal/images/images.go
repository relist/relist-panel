// Package images owns the Images page's HTTP surface: the listing of the
// host's Docker images, each marked with whether a container uses it, and the
// upload of a `docker save` archive that is streamed into the daemon's load
// endpoint. It declares its port — the Images seam — in the consumer, per
// ADR-0003, implemented for real by a Docker Engine API adapter and faked in
// tests by images/faketest. It owns no SQL and touches no disk: an upload is
// never buffered here, only passed through.
package images

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ErrLoadFailed reports that the daemon rejected the archive. Adapters wrap it
// with the daemon's own message: fmt.Errorf("%w: %s", ErrLoadFailed, msg).
var ErrLoadFailed = errors.New("images: load failed")

// ErrUnavailable reports that the Docker Engine API cannot be reached.
var ErrUnavailable = errors.New("images: docker is not reachable")

// UploadCap is the largest archive the panel accepts, hard-coded like the
// Volume Explorer's upload cap.
const UploadCap int64 = 10 << 30

// Image is one image on the host.
type Image struct {
	ID      string
	Tags    []string
	Size    int64
	Created time.Time
	InUse   bool
}

// Images is the seam to the Docker Engine API. size is the archive's declared
// length, so the adapter can send a fixed Content-Length instead of chunking.
type Images interface {
	List(ctx context.Context) ([]Image, error)
	Load(ctx context.Context, archive io.Reader, size int64) ([]string, error)
}

// Service serves the image routes.
type Service struct {
	store Images
}

// New returns a Service over store.
func New(store Images) *Service {
	return &Service{store: store}
}

// RegisterRoutes registers the image routes on mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/images", s.list)
	mux.HandleFunc("PUT /api/images", s.upload)
}

type imageJSON struct {
	ID      string    `json:"id"`
	Tags    []string  `json:"tags"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
	InUse   bool      `json:"inUse"`
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	listed, err := s.store.List(r.Context())
	if err != nil {
		if r.Context().Err() != nil {
			return // the client went away; nobody is left to answer
		}
		s.writeStoreError(w, err)
		return
	}
	sorted := append([]Image(nil), listed...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Created.Equal(sorted[j].Created) {
			return sorted[i].Created.After(sorted[j].Created)
		}
		return sorted[i].ID < sorted[j].ID
	})
	out := make([]imageJSON, 0, len(sorted))
	for _, image := range sorted {
		tags := image.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, imageJSON{ID: image.ID, Tags: tags, Size: image.Size, Created: image.Created, InUse: image.InUse})
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": out})
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "upload requires a declared length (Content-Length)")
		return
	}
	if r.ContentLength > UploadCap {
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds the 10 GiB cap")
		return
	}
	body := http.MaxBytesReader(w, r.Body, r.ContentLength)
	names, err := s.store.Load(r.Context(), body, r.ContentLength)
	if err != nil {
		if r.Context().Err() != nil {
			return // the client went away; nobody is left to answer
		}
		if errors.Is(err, ErrLoadFailed) {
			// A browser still sending a large corrupt archive would see a
			// connection reset instead of the daemon's message.
			_, _ = io.Copy(io.Discard, body)
		}
		s.writeStoreError(w, err)
		return
	}
	if len(names) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "archive contained no images")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": names})
}

func (s *Service) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "Docker is not reachable")
	case errors.Is(err, ErrLoadFailed):
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), ErrLoadFailed.Error()+": "))
	default:
		log.Printf("images: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
