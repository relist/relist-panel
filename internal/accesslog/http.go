package accesslog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logTail is how many lines of Traefik's own log the Traefik page shows.
const logTail = 500

// Flags the install's Traefik is started with (docker-compose.yml): their
// presence says whether this install writes the Access log at all.
const (
	accessLogFlag  = "--accesslog.filepath=/letsencrypt/access/access.log"
	trustedIPsFlag = "--entrypoints.websecure.forwardedheaders.trustedips="
)

type Service struct {
	store   *Store
	dir     string
	traefik Traefik
}

// New serves the Traefik page. A nil traefik reads as no Traefik container.
func New(db *sql.DB, dataDir string, traefik Traefik) *Service {
	return &Service{store: NewStore(db), dir: Dir(dataDir), traefik: traefik}
}

// Rotator returns the Rotator over the same directory and settings.
func (s *Service) Rotator() *Rotator {
	return &Rotator{Dir: s.dir, Store: s.store, Traefik: s.traefik, Now: time.Now, QuietFor: 5 * time.Minute}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/traefik/logs", s.logs)
	mux.HandleFunc("GET /api/traefik/status", s.status)
	mux.HandleFunc("GET /api/traefik/access-logs", s.list)
	mux.HandleFunc("GET /api/traefik/access-logs/{name}", s.download)
	mux.HandleFunc("GET /api/traefik/access-log-settings", s.getSettings)
	mux.HandleFunc("PUT /api/traefik/access-log-settings", s.putSettings)
}

func (s *Service) logsOf(ctx context.Context) (string, error) {
	if s.traefik == nil {
		return "", ErrNoTraefik
	}
	return s.traefik.Logs(ctx, logTail)
}

func (s *Service) argsOf(ctx context.Context) ([]string, error) {
	if s.traefik == nil {
		return nil, ErrNoTraefik
	}
	return s.traefik.Args(ctx)
}

func (s *Service) logs(w http.ResponseWriter, r *http.Request) {
	text, err := s.logsOf(r.Context())
	if errors.Is(err, ErrNoTraefik) {
		writeJSON(w, http.StatusOK, map[string]any{"logs": "", "found": false})
		return
	}
	if err != nil {
		log.Printf("accesslog: traefik logs: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read Traefik's log", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": text, "found": true})
}

func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	args, err := s.argsOf(r.Context())
	if errors.Is(err, ErrNoTraefik) {
		writeJSON(w, http.StatusOK, map[string]any{"found": false, "accessLogEnabled": false, "trustedIps": ""})
		return
	}
	if err != nil {
		log.Printf("accesslog: traefik args: %v", err)
		writeError(w, http.StatusInternalServerError, "could not inspect Traefik", "")
		return
	}
	enabled, trusted := false, ""
	for _, arg := range args {
		if arg == accessLogFlag {
			enabled = true
		}
		if v, ok := strings.CutPrefix(arg, trustedIPsFlag); ok {
			trusted = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": true, "accessLogEnabled": enabled, "trustedIps": trusted})
}

type fileResponse struct {
	Name string  `json:"name"`
	Date *string `json:"date"`
	Size int64   `json:"size"`
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	files, total, err := List(s.dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list Access log files", "")
		return
	}
	out := make([]fileResponse, 0, len(files))
	for _, f := range files {
		resp := fileResponse{Name: f.Name, Size: f.Size}
		if f.Date != "" {
			resp.Date = &f.Date
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out, "totalSize": total})
}

// download serves one file by name. Only names List would show are opened,
// so the route cannot reach outside the directory.
func (s *Service) download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !ValidName(name) {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	contentType := "text/plain; charset=utf-8"
	if strings.HasSuffix(name, ".gz") {
		contentType = "application/gzip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func settingsResponse(v Settings) map[string]any {
	return map[string]any{
		"retentionDays": v.RetentionDays,
		"keepForever":   v.KeepForever,
		"lastRotatedAt": v.LastRotatedAt,
		"lastError":     v.LastError,
	}
}

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read Access log settings", "")
		return
	}
	writeJSON(w, http.StatusOK, settingsResponse(v))
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RetentionDays int  `json:"retentionDays"`
		KeepForever   bool `json:"keepForever"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	if err := s.store.SaveRetention(r.Context(), body.RetentionDays, body.KeepForever); err != nil {
		if errors.Is(err, ErrInvalidRetention) {
			writeError(w, http.StatusBadRequest, err.Error(), "retentionDays")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not save Access log settings", "")
		return
	}
	s.getSettings(w, r)
}

func writeError(w http.ResponseWriter, status int, message, field string) {
	body := map[string]string{"error": message}
	if field != "" {
		body["field"] = field
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
