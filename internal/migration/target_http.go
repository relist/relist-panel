package migration

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// RegisterControlRoutes adds the pull routes to the control socket's mux,
// the only place they are mounted (ADR-0019), unguarded: reaching the socket
// already takes a shell in the container.
func (t *Target) RegisterControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/migration/pull/connect", t.connectRoute)
	mux.HandleFunc("POST /api/migration/pull/recheck", t.recheckRoute)
	mux.HandleFunc("POST /api/migration/pull/start", t.startRoute)
	mux.HandleFunc("GET /api/migration/pull/progress", t.progressRoute)
	mux.HandleFunc("POST /api/migration/pull/retry", t.retryRoute)
	mux.HandleFunc("POST /api/migration/pull/abort", t.abortRoute)
}

// checkSelfURL checks the connect body's selfUrl: a socket request has no
// address of this panel, so the operator gives it.
func checkSelfURL(given string) error {
	if given == "" {
		return errors.New("selfUrl is required: this panel's own URL, which the old server shows as where it moved")
	}
	if u, err := url.Parse(given); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("selfUrl %q is not an http:// or https:// URL", given)
	}
	return nil
}

// connectRoute connects to the source.
func (t *Target) connectRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SourceURL string `json:"sourceUrl"`
		Token     string `json:"token"`
		SelfURL   string `json:"selfUrl"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := checkSelfURL(in.SelfURL); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := t.Connect(r.Context(), in.SourceURL, in.Token, in.SelfURL)
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
