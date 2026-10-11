package sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/volfs"
)

const (
	maxUploadBytes = 500 << 20
	maxBodyBytes   = 1 << 16
	testTimeout    = time.Minute

	msgRunning = "A deployment is running"
)

// SourceLocker is the Source lock a Deployment holds while it fetches or
// builds from src/. unlock must be called exactly once.
type SourceLocker interface {
	TryLockSource(composeID int64) (unlock func(), ok bool)
}

// Gate is the Project gate. It has the same shape as the one the deployment
// worker uses, so sources does not import deployments.
type Gate interface {
	Acquire(ctx context.Context, projectID int64) (func(), error)
}

// Service serves the /source routes of a Compose.
type Service struct {
	store     *Store
	composes  *composes.Service
	git       Git
	locker    SourceLocker
	gate      Gate
	maxUpload int64
}

func New(db *sql.DB, composes *composes.Service, git Git, locker SourceLocker, gate Gate) *Service {
	return &Service{store: NewStore(db), composes: composes, git: git, locker: locker, gate: gate, maxUpload: maxUploadBytes}
}

const base = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/source"

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+base, s.get)
	mux.HandleFunc("PUT "+base, s.put)
	mux.HandleFunc("DELETE "+base, s.delete)
	mux.HandleFunc("POST "+base+"/key", s.regenerateKey)
	mux.HandleFunc("POST "+base+"/test", s.test)
	mux.HandleFunc("POST "+base+"/known-hosts/reset", s.resetKnownHosts)
	mux.HandleFunc("POST "+base+"/upload", s.upload)
	mux.HandleFunc("GET "+base+"/dockerfiles", s.dockerfiles)
}

type lastTestJSON struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
}

type zipJSON struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	Files      int       `json:"files"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// sourceJSON is the Source as the API shows it. It has no private key field.
type sourceJSON struct {
	Kind       Kind          `json:"kind"`
	RepoURL    string        `json:"repoUrl"`
	Branch     string        `json:"branch"`
	Submodules bool          `json:"submodules"`
	PublicKey  string        `json:"publicKey"`
	HeadSHA    string        `json:"headSha"`
	LastTest   *lastTestJSON `json:"lastTest"`
	Zip        *zipJSON      `json:"zip"`
}

func toJSON(src Source) sourceJSON {
	j := sourceJSON{
		Kind: src.Kind, RepoURL: src.RepoURL, Branch: src.Branch, Submodules: src.Submodules,
		PublicKey: src.PublicKey, HeadSHA: src.HeadSHA,
	}
	if src.LastTestAt != nil && src.LastTestOK != nil {
		j.LastTest = &lastTestJSON{At: *src.LastTestAt, OK: *src.LastTestOK}
	}
	if src.Kind == KindZip {
		z := &zipJSON{Name: src.ZipName, Size: src.ZipSize, Files: src.ZipFiles}
		if src.UploadedAt != nil {
			z.UploadedAt = *src.UploadedAt
		}
		j.Zip = z
	}
	return j
}

// target is the Compose a request is about.
type target struct {
	id        int64
	projectID int64
	name      string
	dir       string
}

func (s *Service) resolve(w http.ResponseWriter, r *http.Request) (target, bool) {
	var ids [3]int64
	for i, name := range []string{"projectID", "environmentID", "composeID"} {
		id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "ids must be positive integers")
			return target{}, false
		}
		ids[i] = id
	}
	_, err := s.composes.ComposeRunRequest(r.Context(), ids[0], ids[1], ids[2])
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "compose not found")
		return target{}, false
	case err != nil && !errors.Is(err, composes.ErrAmbiguousIdentity):
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return target{}, false
	}
	t, err := s.composes.Target(r.Context(), ids[2])
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return target{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return target{}, false
	}
	return target{id: ids[2], projectID: t.ProjectID, name: t.Compose.Name, dir: composes.ComposeDir(t.Request)}, true
}

// lock takes the Source lock or answers 409.
func (s *Service) lock(w http.ResponseWriter, id int64) (func(), bool) {
	unlock, ok := s.locker.TryLockSource(id)
	if !ok {
		writeError(w, http.StatusConflict, msgRunning)
		return nil, false
	}
	return unlock, true
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	src, err := s.store.Get(r.Context(), t.id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose has no source")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load source")
		return
	}
	writeJSON(w, http.StatusOK, toJSON(src))
}

type putInput struct {
	Kind       Kind   `json:"kind"`
	RepoURL    string `json:"repoUrl"`
	Branch     string `json:"branch"`
	Submodules bool   `json:"submodules"`
}

func keyComment(composeID int64) string { return fmt.Sprintf("relist-c%d", composeID) }

func (s *Service) put(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	var in putInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if in.Kind != KindGit {
		writeError(w, http.StatusBadRequest, `kind must be "git"; upload a zip to /source/upload`)
		return
	}
	repoURL, err := ValidateRepoURL(in.RepoURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	branch, err := ValidateBranch(in.Branch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	unlock, ok := s.lock(w, t.id)
	if !ok {
		return
	}
	defer unlock()
	if err := EnsureOwnedSrc(t.dir); errors.Is(err, ErrForeignSrc) {
		writeError(w, http.StatusConflict, err.Error())
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not inspect src")
		return
	}

	existing, err := s.store.Get(r.Context(), t.id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "could not load source")
		return
	}
	next := Source{ComposeID: t.id, Kind: KindGit, RepoURL: repoURL, Branch: branch, Submodules: in.Submodules}
	if err == nil && existing.Kind == KindZip {
		// An uploaded tree may carry its own .git/config; git init must never
		// run inside it. EnsureOwnedSrc above already vouched that src/ is ours.
		if err := os.RemoveAll(filepath.Join(t.dir, "src")); err != nil {
			writeError(w, http.StatusInternalServerError, "could not remove the uploaded code")
			return
		}
	}
	if err == nil && existing.Kind == KindGit {
		next.PrivateKey, next.PublicKey, next.CreatedAt = existing.PrivateKey, existing.PublicKey, existing.CreatedAt
		// A test and its commit describe the repository and branch they ran against.
		if existing.RepoURL == repoURL {
			next.LastTestAt, next.LastTestOK = existing.LastTestAt, existing.LastTestOK
			if existing.Branch == branch {
				next.HeadSHA = existing.HeadSHA
			}
		}
	}
	if next.PrivateKey == "" {
		next.PrivateKey, next.PublicKey, err = GenerateKey(keyComment(t.id))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate deploy key")
			return
		}
	}
	if err := s.store.Save(r.Context(), next); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save source")
		return
	}
	s.respond(w, r, t.id)
}

// respond answers with the stored Source.
func (s *Service) respond(w http.ResponseWriter, r *http.Request, composeID int64) {
	src, err := s.store.Get(r.Context(), composeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load source")
		return
	}
	writeJSON(w, http.StatusOK, toJSON(src))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	unlock, ok := s.lock(w, t.id)
	if !ok {
		return
	}
	defer unlock()
	if _, err := s.store.Get(r.Context(), t.id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose has no source")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load source")
		return
	}
	if err := s.store.Delete(r.Context(), t.id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete source")
		return
	}
	// Only a src/ the panel made goes. A foreign one stays where it is.
	if err := EnsureOwnedSrc(t.dir); err == nil {
		if err := os.RemoveAll(filepath.Join(t.dir, "src")); err != nil {
			writeError(w, http.StatusInternalServerError, "source deleted but src/ could not be removed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) regenerateKey(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	unlock, ok := s.lock(w, t.id)
	if !ok {
		return
	}
	defer unlock()
	src, err := s.store.Get(r.Context(), t.id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose has no source")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load source")
		return
	}
	if src.Kind != KindGit {
		writeError(w, http.StatusBadRequest, "only a git source has a deploy key")
		return
	}
	src.PrivateKey, src.PublicKey, err = GenerateKey(keyComment(t.id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate deploy key")
		return
	}
	// The old test used the old key.
	src.LastTestAt, src.LastTestOK, src.HeadSHA = nil, nil, ""
	src.UpdatedAt = time.Time{}
	if err := s.store.Save(r.Context(), src); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save source")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": src.PublicKey})
}

type testInput struct {
	RepoURL string `json:"repoUrl"`
}

type testResult struct {
	OK       bool     `json:"ok"`
	Branches []string `json:"branches"`
	Error    string   `json:"error"`
}

func (s *Service) test(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	var in testInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	src, err := s.store.Get(r.Context(), t.id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && src.Kind != KindGit {
		writeError(w, http.StatusNotFound, "compose has no git source")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load source")
		return
	}
	// A URL in the body is tried with the stored key and never saved, so its
	// outcome says nothing about the stored Source.
	record := in.RepoURL == ""
	if !record {
		if src.RepoURL, err = ValidateRepoURL(in.RepoURL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), testTimeout)
	defer cancel()
	branches, err := s.git.ListBranches(ctx, src, t.dir)
	res := testResult{OK: err == nil, Branches: []string{}}
	headSHA := ""
	if err != nil {
		res.Error = err.Error()
	}
	for _, b := range branches {
		res.Branches = append(res.Branches, b.Name)
		if b.Name == src.Branch {
			headSHA = b.SHA
		}
	}
	if record {
		if err := s.store.RecordTest(r.Context(), t.id, res.OK, headSHA, time.Now()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not record the test")
			return
		}
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Service) resetKnownHosts(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	unlock, ok := s.lock(w, t.id)
	if !ok {
		return
	}
	defer unlock()
	if err := os.Remove(filepath.Join(t.dir, ".ssh", "known_hosts")); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "could not reset known hosts")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) dockerfiles(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	found, err := DetectDockerfiles(filepath.Join(t.dir, "src"), t.name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not scan src")
		return
	}
	type item struct {
		Path    string `json:"path"`
		Service string `json:"service"`
		Context string `json:"context"`
	}
	out := make([]item, 0, len(found))
	for _, d := range found {
		out = append(out, item{Path: d.Path, Service: d.Service, Context: d.Context})
	}
	writeJSON(w, http.StatusOK, out)
}

// badUpload is a request that is not the multipart upload the route expects.
type badUpload struct{ msg string }

func (e badUpload) Error() string { return e.msg }

// stageUpload streams the request's "file" part into a temp zip in dir and
// returns the zip's path, its client file name and its size.
func (s *Service) stageUpload(w http.ResponseWriter, r *http.Request, dir string) (path, name string, size int64, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	reader, err := r.MultipartReader()
	if err != nil {
		return "", "", 0, err
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", "", 0, badUpload{`multipart field "file" is required`}
		} else if err != nil {
			return "", "", 0, err
		}
		if part.FormName() != "file" {
			continue
		}
		tmp, err := os.CreateTemp(dir, ".upload-*.zip")
		if err != nil {
			return "", "", 0, fmt.Errorf("create temp upload: %w", err)
		}
		n, copyErr := io.Copy(tmp, part)
		if closeErr := tmp.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			_ = os.Remove(tmp.Name())
			return "", "", 0, copyErr
		}
		name := filepath.Base(part.FileName())
		if name == "." || name == "/" || name == "" {
			name = "upload.zip"
		}
		return tmp.Name(), name, n, nil
	}
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolve(w, r)
	if !ok {
		return
	}
	// Refuse early, but do not hold the lock while the bytes arrive.
	unlock, ok := s.lock(w, t.id)
	if !ok {
		return
	}
	unlock()

	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the compose directory")
		return
	}
	var tmpZip, staging string
	composeGone := false
	defer func() {
		if tmpZip != "" {
			_ = os.Remove(tmpZip)
		}
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
		if composeGone {
			// The MkdirAll above may have recreated a deleted Compose's
			// directory; Remove only succeeds on an empty one.
			_ = os.Remove(t.dir)
		}
	}()

	tmpZip, name, size, err := s.stageUpload(w, r, t.dir)
	if err != nil {
		var tooBig *http.MaxBytesError
		var bad badUpload
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload is larger than %d MiB", s.maxUpload>>20))
		case errors.As(err, &bad):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusBadRequest, "could not read the upload: "+err.Error())
		}
		return
	}
	// Each request stages in its own directory, so two uploads never mix trees.
	staging, err = os.MkdirTemp(t.dir, ".src.new-*")
	if err == nil {
		err = os.Chmod(staging, 0o755)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the staging directory")
		return
	}
	files, err := ExtractZip(tmpZip, staging, volfs.DefaultExtractLimits())
	switch {
	case errors.Is(err, ErrUnsafeZip), errors.Is(err, volfs.ErrInvalidArchive), errors.Is(err, volfs.ErrArchiveTooLarge):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not extract the zip")
		return
	}

	// The gate first, then the lock, so the lock is never held while waiting.
	releaseGate, err := s.gate.Acquire(r.Context(), t.projectID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not acquire the project lock")
		return
	}
	defer releaseGate()
	unlock, ok = s.lock(w, t.id)
	if !ok {
		return
	}
	defer unlock()
	// A Delete may have run while the bytes streamed; installing now would
	// recreate its directory and then fail to save a row for it.
	if _, err := s.composes.Target(r.Context(), t.id); errors.Is(err, sql.ErrNoRows) {
		composeGone = true
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	if err := EnsureOwnedSrc(t.dir); errors.Is(err, ErrForeignSrc) {
		writeError(w, http.StatusConflict, err.Error())
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not inspect src")
		return
	}
	if err := ReplaceSrc(t.dir, staging); err != nil {
		writeError(w, http.StatusInternalServerError, "could not install the uploaded code")
		return
	}
	now := time.Now()
	row := Source{ComposeID: t.id, Kind: KindZip, ZipName: name, ZipSize: size, ZipFiles: files, UploadedAt: &now}
	if existing, err := s.store.Get(r.Context(), t.id); err == nil {
		row.CreatedAt = existing.CreatedAt
	}
	if err := s.store.Save(r.Context(), row); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save source")
		return
	}
	s.respond(w, r, t.id)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
