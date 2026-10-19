package migration

import (
	"errors"
	"net/http"
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
	mux.HandleFunc("POST /api/migration/pull/connect", guard(t.connectRoute))
	mux.HandleFunc("POST /api/migration/pull/recheck", guard(t.recheckRoute))
	mux.HandleFunc("POST /api/migration/pull/start", guard(t.startRoute))
	mux.HandleFunc("GET /api/migration/pull/progress", guard(t.progressRoute))
	mux.HandleFunc("POST /api/migration/pull/retry", guard(t.retryRoute))
	mux.HandleFunc("POST /api/migration/pull/abort", guard(t.abortRoute))
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

func (t *Target) connectRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SourceURL string `json:"sourceUrl"`
		Token     string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := t.Connect(r.Context(), in.SourceURL, in.Token, selfURL(r))
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
