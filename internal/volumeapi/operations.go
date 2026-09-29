package volumeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
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

// readFile streams one file's bytes straight to the client. `download=1`
// marks the stream an attachment named after the file's base name; without
// it the bytes stay inline, exactly as before.
func (s *Service) readFile(w http.ResponseWriter, r *http.Request) {
	req, _, ok := s.operation(w, r, volumes.OpRead)
	if !ok {
		return
	}

	disposition := ""
	if r.URL.Query().Get("download") == "1" {
		disposition = contentDisposition(filepath.Base(req.Path))
	}
	s.stream(w, r.Context(), req, "application/octet-stream", disposition)
}

// operation validates one operation request and resolves the helper run it
// asks for, or writes the error response itself. The order is the spec's:
// the Compose must exist, the feature must be available, the source must be a
// resolved Volume (never free text), its pre-flight must permit a mount, and
// the user and path must be the shapes this API accepts. It returns the
// matched Volume too, so a download can be named after what it carries.
// Nothing but a Request it returns ever reaches the runner.
func (s *Service) operation(w http.ResponseWriter, r *http.Request, op volumes.Op) (volumes.Request, volumes.Volume, bool) {
	request, ok := s.composeRequest(w, r)
	if !ok {
		return volumes.Request{}, volumes.Volume{}, false
	}

	if err := s.runner.Available(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "volume files unavailable: "+err.Error())
		return volumes.Request{}, volumes.Volume{}, false
	}

	resolution, err := s.resolver.Resolve(r.Context(), volumes.ComposeRef{
		ProjectName:     request.ProjectName,
		ComposeFilePath: request.ComposeFilePath,
		EnvFilePath:     request.EnvFilePath,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "volume resolution failed: "+err.Error())
		return volumes.Request{}, volumes.Volume{}, false
	}

	volume, ok := volumeBySource(resolution.Volumes, r.URL.Query().Get("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown volume")
		return volumes.Request{}, volumes.Volume{}, false
	}

	preflight, err := s.resolver.Preflight(r.Context(), volume)
	if err != nil {
		writeError(w, http.StatusBadGateway, "volume pre-flight failed: "+err.Error())
		return volumes.Request{}, volumes.Volume{}, false
	}
	if preflight.Status == volumes.PreflightMissing {
		writeError(w, http.StatusConflict, "volume "+volume.Source+" is missing: "+preflight.Warning)
		return volumes.Request{}, volumes.Volume{}, false
	}

	user, err := operationUser(r.URL.Query(), volume, resolution.Users)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return volumes.Request{}, volumes.Volume{}, false
	}
	path, err := operationPath(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return volumes.Request{}, volumes.Volume{}, false
	}

	return volumes.Request{
		Source:   volume.Source,
		User:     user,
		Op:       op,
		Path:     path,
		ReadOnly: true,
	}, volume, true
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
	req, _, ok := s.operation(w, r, op)
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

// stream answers read and archive: bytes go from the helper to the client as
// they arrive. Neither the status nor the download headers are committed
// until the first byte, so a run that failed before producing output can
// still be a 502 carrying the helper's message — a plain JSON error with no
// attachment headers on it. Once bytes have flowed the response is committed;
// a later failure can only be logged, because the status was already sent.
func (s *Service) stream(w http.ResponseWriter, ctx context.Context, req volumes.Request, contentType, disposition string) {
	headers := http.Header{"Content-Type": []string{contentType}}
	if disposition != "" {
		headers.Set("Content-Disposition", disposition)
	}
	out := &statusWriter{w: w, headers: headers}
	err := s.runner.Run(ctx, req, nil, out)
	if err != nil {
		if out.wrote {
			log.Printf("volume %s failed after bytes were sent: %v", req.Op, err)
			return
		}
		writeError(w, http.StatusBadGateway, "volume "+string(req.Op)+" failed: "+err.Error())
		return
	}
	if !out.wrote {
		out.commit()
	}
}

// statusWriter delays the status — and the response headers it carries —
// until the helper's first byte arrives: a run that fails before producing
// output can still answer with an error status and a plain JSON body, while
// once a byte has flowed the status is committed and nothing can change it.
// Only Write and commit are needed — the runner's streaming contract is an
// io.Writer.
type statusWriter struct {
	w       http.ResponseWriter
	headers http.Header
	wrote   bool
}

// commit applies the held headers and sends 200, once. The first byte and a
// successful run that produced none both call it.
func (s *statusWriter) commit() {
	if s.wrote {
		return
	}
	s.wrote = true
	for name, values := range s.headers {
		for _, value := range values {
			s.w.Header().Add(name, value)
		}
	}
	s.w.WriteHeader(http.StatusOK)
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		s.commit()
	}
	return s.w.Write(p)
}
