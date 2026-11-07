package migration

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"relist-panel/internal/safely"
)

var tokenLifetimes = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "168h": 168 * time.Hour}

// RegisterRoutes adds the old host's web routes: those that move data to the
// new host, public to the cookie middleware and checking the Migration
// session key themselves, and the status the lock page reads with the admin
// cookie. Creating a token and resuming are the control socket's alone
// (ADR-0019), so a stolen web session cannot start a Migration.
func (s *OldHost) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/migration/claim", s.claim)
	mux.HandleFunc("GET /api/migration/preflight", s.session(s.preflight))
	mux.HandleFunc("POST /api/migration/start", s.session(s.startRoute))
	mux.HandleFunc("GET /api/migration/items/{id}", s.session(s.item))
	mux.HandleFunc("GET /api/migration/items/{id}/digest", s.session(s.digest))
	mux.HandleFunc("POST /api/migration/abort", s.session(s.abort))
	mux.HandleFunc("POST /api/migration/finish", s.session(s.finish))
	mux.HandleFunc("GET /api/migration/status", s.status)
}

// RegisterControlRoutes adds the old host's admin routes to the control
// socket's mux (ADR-0019), their only home: creating and revoking a token,
// the status, resuming and the activity. They take no cookie, since reaching
// the socket already takes a shell in the container.
func (s *OldHost) RegisterControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/migration/tokens", s.createToken)
	mux.HandleFunc("DELETE /api/migration/tokens/current", s.revokeToken)
	mux.HandleFunc("GET /api/migration/status", s.status)
	mux.HandleFunc("POST /api/migration/resume", s.resume)
	mux.HandleFunc("GET /api/migration/serve/activity", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Activity())
	})
}

// OldHostStatus is the old host's own view of its Migration, for its lock
// page and `relist-panel migration`.
type OldHostStatus struct {
	Step           Step       `json:"step"`
	ClaimedBy      string     `json:"claimedBy"`
	TargetURL      string     `json:"targetUrl"`
	TokenExpiresAt *time.Time `json:"tokenExpiresAt"`
	LockedAt       *time.Time `json:"lockedAt"`
	MigratedAt     *time.Time `json:"migratedAt"`
}

// SessionPath reports the routes the cookie middleware must let through: the
// old host's routes that authenticate with the Migration session key.
func SessionPath(path string) bool {
	switch path {
	case "/api/migration/claim", "/api/migration/preflight", "/api/migration/start", "/api/migration/abort", "/api/migration/finish":
		return true
	}
	return strings.HasPrefix(path, "/api/migration/items/")
}

func (s *OldHost) session(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || key == "" {
			writeJSONError(w, http.StatusUnauthorized, ErrInvalidSession.Error())
			return
		}
		if err := s.d.Store.VerifySession(r.Context(), key); err != nil {
			if errors.Is(err, ErrInvalidSession) {
				writeJSONError(w, http.StatusUnauthorized, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
			return
		}
		next(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *OldHost) claim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token     string `json:"token"`
		ClaimedBy string `json:"claimedBy"`
	}
	if !decode(w, r, &in) {
		return
	}
	key, err := s.d.Store.Claim(r.Context(), in.Token, in.ClaimedBy)
	switch {
	case errors.Is(err, ErrInvalidToken):
		writeJSONError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, ErrAlreadyClaimed):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrClaimLocked):
		w.Header().Set("Retry-After", "60")
		writeJSONError(w, http.StatusTooManyRequests, err.Error())
	case err != nil:
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"sessionKey": key})
	}
}

func (s *OldHost) preflight(w http.ResponseWriter, r *http.Request) {
	info, err := s.Info(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// startRoute answers 200 with the manifest once the lock is ready, and 202 while
// the lock sequence runs in the background, detached from the request.
func (s *OldHost) startRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostPaths       []string `json:"hostPaths"`
		IncludeRegistry bool     `json:"includeRegistry"`
	}
	if !decode(w, r, &in) {
		return
	}
	step, st, err := s.d.Store.Lock(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
		return
	}
	if (step == StepReady || step == StepMigrated) && st.Manifest != nil {
		writeJSON(w, http.StatusOK, st.Manifest)
		return
	}
	s.startMu.Lock()
	lastErr := s.startErr
	if !s.starting {
		s.starting, s.startErr = true, nil
		go func() {
			err := safely.Call("migration lock", func() error {
				_, err := s.StartWith(context.WithoutCancel(r.Context()), in.HostPaths, in.IncludeRegistry)
				return err
			})
			if err != nil {
				log.Printf("migration: start: %v", err)
			}
			s.startMu.Lock()
			s.starting, s.startErr = false, err
			s.startMu.Unlock()
		}()
	}
	s.startMu.Unlock()
	body := map[string]string{"step": string(step)}
	if lastErr != nil {
		body["error"] = lastErr.Error()
	}
	writeJSON(w, http.StatusAccepted, body)
}

func (s *OldHost) item(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	step, st, err := s.d.Store.Lock(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
		return
	}
	if (step != StepReady && step != StepMigrated) || st.Manifest == nil || !s.settled.Load() {
		writeJSONError(w, http.StatusConflict, ErrNotReady.Error())
		return
	}
	known := false
	for _, it := range st.Manifest.Items {
		known = known || it.ID == id
	}
	if !known {
		writeJSONError(w, http.StatusNotFound, ErrUnknownItem.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := s.StreamItem(r.Context(), id, w); err != nil {
		// The status is already sent; a short body and no digest tell the
		// new host the stream failed.
		log.Printf("migration: %v", err)
		panic(http.ErrAbortHandler)
	}
}

func (s *OldHost) digest(w http.ResponseWriter, r *http.Request) {
	d, ok := s.Digest(r.PathValue("id"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "no complete stream of this item yet")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sha256": d})
}

func (s *OldHost) abort(w http.ResponseWriter, r *http.Request) {
	if err := s.Resume(r.Context()); err != nil {
		var unstarted *UnstartedError
		if errors.As(err, &unstarted) {
			writeJSON(w, http.StatusOK, map[string]string{"warning": err.Error()})
			return
		}
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *OldHost) finish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TargetURL string `json:"targetUrl"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !webURL(in.TargetURL) {
		writeJSONError(w, http.StatusBadRequest, "targetUrl must be an http:// or https:// URL")
		return
	}
	if err := s.Finish(r.Context(), in.TargetURL); err != nil {
		if errors.Is(err, ErrNotReady) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// webURL reports an absolute http or https URL with a host: the old host's
// lock page shows the new host's URL as a link.
func webURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *OldHost) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Lifetime string `json:"lifetime"`
	}
	if !decode(w, r, &in) {
		return
	}
	lifetime, ok := tokenLifetimes[in.Lifetime]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, ErrBadLifetime.Error())
		return
	}
	token, expires, err := s.d.Store.CreateToken(r.Context(), lifetime)
	if errors.Is(err, ErrStarted) {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expiresAt": expires})
}

func (s *OldHost) revokeToken(w http.ResponseWriter, r *http.Request) {
	err := s.d.Store.RevokeToken(r.Context())
	if errors.Is(err, ErrStarted) {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *OldHost) status(w http.ResponseWriter, r *http.Request) {
	step, st, err := s.d.Store.Lock(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
		return
	}
	expires, claimedBy, err := s.d.Store.TokenStatus(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
		return
	}
	if st.ClaimedBy != "" {
		claimedBy = st.ClaimedBy
	}
	out := OldHostStatus{Step: step, ClaimedBy: claimedBy, TargetURL: st.TargetURL, TokenExpiresAt: expires}
	if st.LockedAt != 0 {
		at := time.Unix(st.LockedAt, 0).UTC()
		out.LockedAt = &at
	}
	if st.MigratedAt != 0 {
		at := time.Unix(st.MigratedAt, 0).UTC()
		out.MigratedAt = &at
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *OldHost) resume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Confirm != "RESUME" {
		writeJSONError(w, http.StatusBadRequest, "type RESUME to confirm")
		return
	}
	if err := s.Resume(r.Context()); err != nil {
		var unstarted *UnstartedError
		if errors.As(err, &unstarted) {
			writeJSON(w, http.StatusOK, map[string]string{"warning": err.Error()})
			return
		}
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
