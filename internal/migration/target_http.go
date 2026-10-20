package migration

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// RegisterRoutes adds the new host's pull routes. allowed decides who may
// call them: anyone before an admin exists, the signed-in admin after.
func (t *Target) RegisterRoutes(mux *http.ServeMux, allowed func(r *http.Request) (bool, error)) {
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ok, err := allowed(r)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
				return
			}
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/migration/pull/connect", guard(t.connectRoute(func(r *http.Request, _ string) (string, error) {
		return selfURL(r), nil
	})))
	mux.HandleFunc("POST /api/migration/pull/recheck", guard(t.recheckRoute))
	mux.HandleFunc("POST /api/migration/pull/start", guard(t.startRoute))
	mux.HandleFunc("GET /api/migration/pull/progress", guard(t.progressRoute))
	mux.HandleFunc("POST /api/migration/pull/retry", guard(t.retryRoute))
	mux.HandleFunc("POST /api/migration/pull/abort", guard(t.abortRoute))
}

// RegisterControlRoutes adds the pull routes to the control socket's mux
// (ADR-0019), unguarded: reaching the socket already takes a shell in the
// container. A socket request has no address of this panel, so connect
// takes it in the body as selfUrl.
func (t *Target) RegisterControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/migration/pull/connect", t.connectRoute(func(_ *http.Request, given string) (string, error) {
		if given == "" {
			return "", errors.New("selfUrl is required: this panel's own URL, which the old server shows as where it moved")
		}
		if u, err := url.Parse(given); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf("selfUrl %q is not an http:// or https:// URL", given)
		}
		return given, nil
	}))
	mux.HandleFunc("POST /api/migration/pull/recheck", t.recheckRoute)
	mux.HandleFunc("POST /api/migration/pull/start", t.startRoute)
	mux.HandleFunc("GET /api/migration/pull/progress", t.progressRoute)
	mux.HandleFunc("POST /api/migration/pull/retry", t.retryRoute)
	mux.HandleFunc("POST /api/migration/pull/abort", t.abortRoute)
}

// selfURL is the address the operator reached this panel on, which the old
// host shows as where it was migrated to.
func selfURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// connectRoute connects to the source; self decides this panel's own URL
// from the request and the body's selfUrl.
func (t *Target) connectRoute(self func(r *http.Request, given string) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			SourceURL string `json:"sourceUrl"`
			Token     string `json:"token"`
			SelfURL   string `json:"selfUrl"`
		}
		if !decode(w, r, &in) {
			return
		}
		own, err := self(r, in.SelfURL)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		p, err := t.Connect(r.Context(), in.SourceURL, in.Token, own)
		if err != nil {
			var remote *remoteError
			if errors.As(err, &remote) && remote.Status < 500 {
				writeJSONError(w, http.StatusBadRequest, remote.Message)
				return
			}
			if errors.Is(err, errBusy) {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			if err.Error() == "The source must use https://" {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func (t *Target) recheckRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostPaths []string `json:"hostPaths"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := t.Recheck(r.Context(), in.HostPaths)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (t *Target) startRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostPaths []string `json:"hostPaths"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := t.Start(r.Context(), in.HostPaths); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, t.Progress())
}

func (t *Target) progressRoute(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, t.Progress())
}

func (t *Target) retryRoute(w http.ResponseWriter, r *http.Request) {
	if err := t.Retry(r.Context()); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, t.Progress())
}

func (t *Target) abortRoute(w http.ResponseWriter, r *http.Request) {
	if err := t.Abort(r.Context()); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
