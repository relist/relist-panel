package volumeapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"relist-panel/internal/volumes"
)

// userPattern is the only shape an explicit `user` query may take: the
// `<uid>:<gid>` pair `--user` receives. No names, no free text.
var userPattern = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

// fallbackUser is the identity the resolution reports for a Service that
// declares no user: exactly the value `volumes` synthesizes (ADR-0010's root
// opt-in, never a default), repeated here only for the impossible case of a
// Volume whose first use resolves to no Service.
const fallbackUser = "0:0"

// listFiles serves one directory listing: the helper's `list` JSON.
func (s *Service) listFiles(w http.ResponseWriter, r *http.Request) {
	s.metadata(w, r, volumes.OpList)
}

// statFile serves one entry's metadata: the helper's `stat` JSON.
func (s *Service) statFile(w http.ResponseWriter, r *http.Request) {
	s.metadata(w, r, volumes.OpStat)
}

// readFile streams one file's bytes straight to the client.
func (s *Service) readFile(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, volumes.OpRead)
}

// operation validates one operation request and resolves the helper run it
// asks for, or writes the error response itself. The order is the spec's:
// the Compose must exist, the feature must be available, the source must be a
// resolved Volume (never free text), its pre-flight must permit a mount, and
// the user and path must be the shapes this API accepts. Nothing but a
// Request it returns ever reaches the runner.
func (s *Service) operation(w http.ResponseWriter, r *http.Request, op volumes.Op) (volumes.Request, bool) {
	request, ok := s.composeRequest(w, r)
	if !ok {
		return volumes.Request{}, false
	}

	if err := s.runner.Available(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "volume files unavailable: "+err.Error())
		return volumes.Request{}, false
	}

	resolution, err := s.resolver.Resolve(r.Context(), volumes.ComposeRef{
		ProjectName:     request.ProjectName,
		ComposeFilePath: request.ComposeFilePath,
		EnvFilePath:     request.EnvFilePath,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "volume resolution failed: "+err.Error())
		return volumes.Request{}, false
	}

	volume, ok := volumeBySource(resolution.Volumes, r.URL.Query().Get("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown volume")
		return volumes.Request{}, false
	}

	preflight, err := s.resolver.Preflight(r.Context(), volume)
	if err != nil {
		writeError(w, http.StatusBadGateway, "volume pre-flight failed: "+err.Error())
		return volumes.Request{}, false
	}
	if preflight.Status == volumes.PreflightMissing {
		writeError(w, http.StatusConflict, "volume "+volume.Source+" is missing: "+preflight.Warning)
		return volumes.Request{}, false
	}

	user, err := operationUser(r.URL.Query(), volume, resolution.Users)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return volumes.Request{}, false
	}
	path, err := operationPath(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return volumes.Request{}, false
	}

	return volumes.Request{
		Source:   volume.Source,
		User:     user,
		Op:       op,
		Path:     path,
		ReadOnly: true,
	}, true
}

// volumeBySource matches a request's source against the resolution's exact
// resolved sources. This is the one invariant the spec makes load-bearing: a
// source is never free text, only a Volume the Compose itself declared, so no
// request can point an operation at an arbitrary host path.
func volumeBySource(resolved []volumes.Volume, source string) (volumes.Volume, bool) {
	for _, volume := range resolved {
		if volume.Source == source {
			return volume, true
		}
	}
	return volumes.Volume{}, false
}

// operationUser applies the one UID/GID rule (spec: UID/GID): an explicit
// `user` query wins; otherwise the named `service`'s resolved user; otherwise
// the resolved user of the Volume's first use — the resolution's own 0:0
// fallback included. Anything else is refused rather than guessed.
func operationUser(query url.Values, volume volumes.Volume, users []volumes.ServiceUser) (string, error) {
	if explicit := query.Get("user"); explicit != "" {
		if !userPattern.MatchString(explicit) {
			return "", errors.New("user must be <uid>:<gid>")
		}
		return explicit, nil
	}

	if service := query.Get("service"); service != "" {
		for _, candidate := range users {
			if candidate.Service == service {
				return candidate.User, nil
			}
		}
		return "", fmt.Errorf("unknown service %q", service)
	}

	if len(volume.Uses) > 0 {
		for _, candidate := range users {
			if candidate.Service == volume.Uses[0].Service {
				return candidate.User, nil
			}
		}
	}
	return fallbackUser, nil
}

// operationPath interprets the path query exactly one way: absent or empty is
// the Volume root, an absolute path is refused before any helper run, and
// everything else is passed through untouched — there is no shell between
// the request and the helper (ADR-0010).
func operationPath(query url.Values) (string, error) {
	path := query.Get("path")
	if path == "" {
		return ".", nil
	}
	if strings.HasPrefix(path, "/") {
		return "", errors.New("path must be relative to the volume root")
	}
	return path, nil
}

// metadata answers list and stat: the helper's JSON. stdout is buffered so a
// failed run can still be reported with the helper's own stderr, and the
// output is validated so garbage is a Bad Gateway rather than a lie forwarded
// to the UI.
func (s *Service) metadata(w http.ResponseWriter, r *http.Request, op volumes.Op) {
	req, ok := s.operation(w, r, op)
	if !ok {
		return
	}

	var stdout bytes.Buffer
	if err := s.runner.Run(r.Context(), req, nil, &stdout); err != nil {
		writeError(w, http.StatusBadGateway, "volume "+string(op)+" failed: "+err.Error())
		return
	}
	if !json.Valid(stdout.Bytes()) {
		writeError(w, http.StatusBadGateway, "volume "+string(op)+" returned invalid JSON")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stdout.Bytes())
}

// stream answers read: bytes go from the helper to the client as they arrive,
// and the 200 status waits for the first byte so a run that failed before
// producing output can still be a 502 carrying the helper's message. Once
// bytes have flowed the response is committed; a later failure can only be
// logged, because the status was already sent.
func (s *Service) stream(w http.ResponseWriter, r *http.Request, op volumes.Op) {
	req, ok := s.operation(w, r, op)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	out := &statusWriter{w: w}
	err := s.runner.Run(r.Context(), req, nil, out)
	if err != nil {
		if out.wrote {
			log.Printf("volume %s failed after bytes were sent: %v", op, err)
			return
		}
		writeError(w, http.StatusBadGateway, "volume "+string(op)+" failed: "+err.Error())
		return
	}
	if !out.wrote {
		w.WriteHeader(http.StatusOK)
	}
}

// statusWriter delays the 200 until the helper's first byte arrives: a run
// that fails before producing output can still answer with an error status,
// while once a byte has flowed the status is committed and nothing can change
// it. Only Write is needed — the runner's streaming contract is an io.Writer.
type statusWriter struct {
	w     http.ResponseWriter
	wrote bool
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && !s.wrote {
		s.wrote = true
		s.w.WriteHeader(http.StatusOK)
	}
	return s.w.Write(p)
}
