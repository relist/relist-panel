package backupdest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"relist-panel/internal/audit"
)

// Service serves the Backup destinations page and opens destinations for the
// Databases' uploads. Every route is admin-only (authz.Routes leaves them out).
type Service struct {
	store *Store
	// connect builds the client of a destination; connect (by type) unless a
	// test swaps it.
	connect func(Destination) (Remote, error)
	now     func() time.Time
	// RemoveFiles removes a deleted Database's files when deleting a
	// destination took its last Backup; nil = nothing to remove.
	removeFiles func(databaseID int64) error
}

func New(store *Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, connect: connect, now: now}
}

// connect builds the client for d's type. Nothing is sent until a call.
func connect(d Destination) (Remote, error) {
	if d.Type == TypeSFTP {
		return NewSFTP(d)
	}
	return NewS3(d)
}

// WithFileRemover sets what removes a deleted Database's files.
// testTimeout bounds a destination Test.
const testTimeout = time.Minute

func (s *Service) WithFileRemover(fn func(databaseID int64) error) *Service {
	s.removeFiles = fn
	return s
}

// Open implements Opener.
func (s *Service) Open(ctx context.Context, id int64) (Destination, Remote, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return Destination{}, nil, err
	}
	r, err := s.connect(d)
	if err != nil {
		return Destination{}, nil, err
	}
	return d, r, nil
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backup-destinations", s.list)
	mux.HandleFunc("POST /api/backup-destinations", s.create)
	mux.HandleFunc("PUT /api/backup-destinations/{id}", s.update)
	mux.HandleFunc("DELETE /api/backup-destinations/{id}", s.delete)
	mux.HandleFunc("POST /api/backup-destinations/{id}/test", s.test)
	mux.HandleFunc("POST /api/backup-destinations/{id}/reset-host-key", s.resetHostKey)
	mux.HandleFunc("GET /api/backup-destinations/{id}/passphrase", s.revealPassphrase)
}

// S3JSON is the settings of an s3 destination as the browser sees them: no secret.
type S3JSON struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"accessKeyId"`
	PathStyle   bool   `json:"pathStyle"`
}

// SFTPJSON is the settings of an sftp destination as the browser sees them:
// no password, no private key. PublicKey and HostKey are response only.
type SFTPJSON struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Path       string `json:"path"`
	Auth       string `json:"auth"`
	SSHKeyID   *int64 `json:"sshKeyId"`
	SSHKeyName string `json:"sshKeyName"`
	PublicKey  string `json:"publicKey"`
	// HostKey is "<type> SHA256:…", or "" while none is trusted.
	HostKey string `json:"hostKey"`
}

// DestinationJSON is a destination as the browser sees it. It never carries the secret.
type DestinationJSON struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	S3            *S3JSON    `json:"s3,omitempty"`
	SFTP          *SFTPJSON  `json:"sftp,omitempty"`
	Encrypt       bool       `json:"encrypt"`
	LastTestAt    *time.Time `json:"lastTestAt"`
	LastTestOK    bool       `json:"lastTestOk"`
	LastTestError string     `json:"lastTestError"`
}

func toJSON(d Destination) DestinationJSON {
	out := DestinationJSON{ID: d.ID, Name: d.Name, Type: d.Type, Encrypt: d.Encrypt,
		LastTestAt: d.LastTestAt, LastTestOK: d.LastTestOK, LastTestError: d.LastTestError}
	if d.Type == TypeSFTP {
		out.SFTP = &SFTPJSON{Host: d.SFTP.Host, Port: d.SFTP.Port, User: d.SFTP.User, Path: d.SFTP.Path, Auth: d.SFTP.Auth,
			SSHKeyID: d.SSHKeyID, SSHKeyName: d.SSHKeyName, PublicKey: d.PublicKey, HostKey: HostKeyLabel(d.HostKey)}
		if d.SFTP.Auth != AuthKey {
			out.SFTP.PublicKey = ""
		}
	} else {
		s3 := S3JSON(d.S3)
		out.S3 = &s3
	}
	return out
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list Backup destinations")
		return
	}
	out := make([]DestinationJSON, 0, len(list))
	for _, d := range list {
		out = append(out, toJSON(d))
	}
	writeJSON(w, http.StatusOK, out)
}

type input struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	S3         S3JSON   `json:"s3"`
	SFTP       SFTPJSON `json:"sftp"`
	Secret     string   `json:"secret"`
	Encrypt    bool     `json:"encrypt"`
	Passphrase string   `json:"passphrase"`
}

func writeFormError(w http.ResponseWriter, err error) {
	var fe *FieldError
	if errors.As(err, &fe) {
		writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
	} else {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// readInput decodes and checks a form; requireSecret is false on edit, where
// an empty secret keeps the stored one. wantType is the stored type on edit
// ("" on create): a destination's type never changes.
func (s *Service) readInput(w http.ResponseWriter, r *http.Request, requireSecret bool, wantType string) (Destination, bool) {
	var in input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return Destination{}, false
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "name", "name is required")
		return Destination{}, false
	}
	if wantType != "" && in.Type != wantType {
		writeFieldError(w, http.StatusUnprocessableEntity, "type", "the type of a Backup destination cannot change")
		return Destination{}, false
	}
	secret := strings.Trim(in.Secret, "\r\n")
	pass := strings.TrimSpace(in.Passphrase)
	if !in.Encrypt {
		// The store forgets the stored passphrase when encryption is off.
		pass = ""
	} else if (requireSecret || pass != "") && utf8.RuneCountInString(pass) < 20 {
		// On edit an empty passphrase keeps the stored one, like the secret.
		writeFieldError(w, http.StatusUnprocessableEntity, "passphrase", "the passphrase must be at least 20 characters")
		return Destination{}, false
	}
	switch in.Type {
	case TypeS3:
		cfg, err := ValidateS3(S3Config(in.S3))
		if err != nil {
			writeFormError(w, err)
			return Destination{}, false
		}
		if requireSecret && secret == "" {
			writeFieldError(w, http.StatusUnprocessableEntity, "secret", "secret access key is required")
			return Destination{}, false
		}
		return Destination{Name: name, Type: TypeS3, S3: cfg, Secret: secret, Encrypt: in.Encrypt, Passphrase: pass}, true
	case TypeSFTP:
		cfg, err := ValidateSFTP(SFTPConfig{Host: in.SFTP.Host, Port: in.SFTP.Port, User: in.SFTP.User, Path: in.SFTP.Path, Auth: in.SFTP.Auth})
		if err != nil {
			writeFormError(w, err)
			return Destination{}, false
		}
		d := Destination{Name: name, Type: TypeSFTP, SFTP: cfg, Secret: secret, Encrypt: in.Encrypt, Passphrase: pass}
		if cfg.Auth == AuthKey {
			d.Secret = ""
			if in.SFTP.SSHKeyID == nil || !s.keyExists(r.Context(), *in.SFTP.SSHKeyID) {
				writeFieldError(w, http.StatusUnprocessableEntity, "sshKeyId", "choose an SSH key")
				return Destination{}, false
			}
			d.SSHKeyID = in.SFTP.SSHKeyID
		} else if requireSecret && secret == "" {
			writeFieldError(w, http.StatusUnprocessableEntity, "secret", "password is required")
			return Destination{}, false
		}
		return d, true
	}
	writeFieldError(w, http.StatusUnprocessableEntity, "type", `type must be "s3" or "sftp"`)
	return Destination{}, false
}

func (s *Service) keyExists(ctx context.Context, id int64) bool {
	var one int
	return s.store.db.QueryRowContext(ctx, `SELECT 1 FROM ssh_keys WHERE id = ?`, id).Scan(&one) == nil
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	in, ok := s.readInput(w, r, true, "")
	if !ok {
		return
	}
	d, err := s.store.Create(r.Context(), in, s.now())
	if err != nil {
		writeStoreError(w, err, "could not save the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	writeJSON(w, http.StatusCreated, toJSON(d))
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	old, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	in, ok := s.readInput(w, r, false, old.Type)
	if !ok {
		return
	}
	in.ID = id
	// Encryption without any passphrase, sent or stored, would encrypt with
	// '' and fail every copy: refuse it here, not only in the form.
	if in.Encrypt && in.Passphrase == "" && old.Passphrase == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "passphrase", "the passphrase must be at least 20 characters")
		return
	}
	// A kept secret stays with the endpoint it was given for: it is never
	// sent to another host.
	if old.Type == TypeS3 && in.Secret == "" && old.S3.Endpoint != in.S3.Endpoint {
		writeFieldError(w, http.StatusUnprocessableEntity, "secret", "enter the secret access key again when changing the endpoint")
		return
	}
	if old.Type == TypeSFTP && in.SFTP.Auth == AuthPassword && in.Secret == "" &&
		(old.SFTP.Auth != AuthPassword || old.SFTP.Host != in.SFTP.Host || old.SFTP.Port != in.SFTP.Port) {
		writeFieldError(w, http.StatusUnprocessableEntity, "secret", "enter the password again")
		return
	}
	if err := s.store.Update(r.Context(), in, s.now()); err != nil {
		writeStoreError(w, err, "could not save the Backup destination")
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	writeJSON(w, http.StatusOK, toJSON(d))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	confirm := -1
	if raw := r.URL.Query().Get("confirm"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			confirm = n
		}
	}
	removed, err := s.store.Delete(r.Context(), id, confirm)
	var inUse ErrInUse
	var copies ErrCopies
	switch {
	case errors.As(err, &inUse):
		writeJSON(w, http.StatusConflict, map[string]any{"error": inUse.Error(), "schedules": inUse.Schedules})
		return
	case errors.As(err, &copies):
		writeJSON(w, http.StatusConflict, map[string]any{"error": copies.Error(), "copies": copies.Copies})
		return
	case err != nil:
		writeStoreError(w, err, "could not delete the Backup destination")
		return
	}
	if s.removeFiles != nil {
		for _, dbID := range removed {
			_ = s.removeFiles(dbID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// test writes, reads and deletes a small object, and records the result. A
// failed test is an answer, not an error: 200 with the failed result stored.
func (s *Service) test(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	msg := ""
	if remote, err := s.connect(d); err != nil {
		msg = scrub(scrub(err.Error(), d.Secret), d.Passphrase)
	} else {
		// A bucket that does not answer fails the test instead of hanging it.
		ctx, cancel := context.WithTimeout(r.Context(), testTimeout)
		err := remote.Test(ctx)
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil && timedOut {
			msg = fmt.Sprintf("no answer within %s", testTimeout)
		} else if err != nil {
			msg = scrub(scrub(err.Error(), d.Secret), d.Passphrase)
		} else if rec, ok := remote.(HostKeyRecorder); ok {
			// The first successful Test trusts the key the server showed.
			if line := rec.SeenHostKey(); line != "" {
				if err := s.store.SetHostKey(r.Context(), id, line); err != nil {
					writeStoreError(w, err, "could not record the host key")
					return
				}
				audit.Detail(r.Context(), "hostKey", HostKeyLabel(line))
			}
		}
	}
	if err := s.store.SetTestResult(r.Context(), id, msg == "", msg, s.now()); err != nil {
		writeStoreError(w, err, "could not record the test")
		return
	}
	d, err = s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	writeJSON(w, http.StatusOK, toJSON(d))
}

// resetHostKey makes an SFTP destination forget its trusted host key and its
// last Test result; the next Test trusts the key the server then presents.
func (s *Service) resetHostKey(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	if d.Type != TypeSFTP {
		writeError(w, http.StatusUnprocessableEntity, "only SFTP destinations have a host key")
		return
	}
	if err := s.store.ResetHostKey(r.Context(), id); err != nil {
		writeStoreError(w, err, "could not reset the host key")
		return
	}
	d, err = s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	writeJSON(w, http.StatusOK, toJSON(d))
}

// revealPassphrase answers the stored passphrase, audited as a secret read.
func (s *Service) revealPassphrase(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	if !d.Encrypt {
		writeError(w, http.StatusUnprocessableEntity, "this destination does not encrypt copies")
		return
	}
	audit.Emit(r.Context(), "secret.read", map[string]any{"keys": []string{"passphrase"}})
	writeJSON(w, http.StatusOK, map[string]string{"passphrase": d.Passphrase})
}

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrDuplicateName):
		writeFieldError(w, http.StatusConflict, "name", err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, fallback)
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

func writeFieldError(w http.ResponseWriter, status int, field, message string) {
	writeJSON(w, status, map[string]string{"error": message, "field": field})
}
