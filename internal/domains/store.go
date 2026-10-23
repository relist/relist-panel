package domains

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
)

const maxRequestBody = 1 << 20

// ErrNotFound means the Compose or the domain does not exist under the route.
var ErrNotFound = errors.New("not found")

// ConflictError means another domain already claims the host and path.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

// Store keeps domains in SQLite.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a Store over the migrated database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

const domainColumns = `id, compose_id, host, path, internal_path, strip_path, service, port, entrypoint, https, cert_provider, cert_resolver, middlewares, created_at, updated_at`

func scanDomain(scan func(...any) error) (Domain, error) {
	var (
		d                   Domain
		strip, https        int
		middlewares         string
		createdAt, updateAt int64
	)
	if err := scan(&d.ID, &d.ComposeID, &d.Host, &d.Path, &d.InternalPath, &strip, &d.Service, &d.Port, &d.Entrypoint, &https, &d.CertProvider, &d.CertResolver, &middlewares, &createdAt, &updateAt); err != nil {
		return Domain{}, err
	}
	d.StripPath = strip == 1
	d.HTTPS = https == 1
	d.Middlewares = []string{}
	if err := json.Unmarshal([]byte(middlewares), &d.Middlewares); err != nil {
		return Domain{}, fmt.Errorf("decode middlewares of domain %d: %w", d.ID, err)
	}
	if d.Middlewares == nil {
		d.Middlewares = []string{}
	}
	d.CreatedAt = time.Unix(createdAt, 0).UTC()
	d.UpdatedAt = time.Unix(updateAt, 0).UTC()
	return d, nil
}

// ListForCompose returns a Compose's domains in ID order.
func (s *Store) ListForCompose(ctx context.Context, composeID int64) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains WHERE compose_id = ? ORDER BY id ASC`, composeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// ListAll returns every Compose's domains in ID order, for the maintenance
// page a Migration puts up (ADR-0018).
func (s *Store) ListAll(ctx context.Context) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// composeYAML returns the saved YAML of a Compose that lives under the given
// Project and Environment, or ErrNotFound.
func (s *Store) composeYAML(ctx context.Context, projectID, environmentID, composeID int64) (string, error) {
	var yamlContent string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.yaml_content
		FROM composes c
		JOIN environments e ON e.id = c.environment_id
		WHERE c.id = ? AND e.id = ? AND e.project_id = ?
	`, composeID, environmentID, projectID).Scan(&yamlContent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return yamlContent, err
}

func (s *Store) create(ctx context.Context, composeID int64, in Input) (Domain, error) {
	middlewares, _ := json.Marshal(in.Middlewares)
	at := s.now().Unix()
	var id int64
	err := s.inTx(ctx, composeID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO domains (compose_id, host, path, internal_path, strip_path, service, port, entrypoint, https, cert_provider, cert_resolver, middlewares, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, composeID, in.Host, in.Path, in.InternalPath, boolInt(in.StripPath), in.Service, in.Port, in.Entrypoint, boolInt(in.HTTPS), in.CertProvider, in.CertResolver, string(middlewares), at, at)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	}, at)
	if err != nil {
		return Domain{}, s.explain(ctx, err, in)
	}
	return s.get(ctx, composeID, id)
}

func (s *Store) update(ctx context.Context, composeID, id int64, in Input) (Domain, error) {
	middlewares, _ := json.Marshal(in.Middlewares)
	at := s.now().Unix()
	err := s.inTx(ctx, composeID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE domains
			SET host = ?, path = ?, internal_path = ?, strip_path = ?, service = ?, port = ?, entrypoint = ?, https = ?, cert_provider = ?, cert_resolver = ?, middlewares = ?, updated_at = ?
			WHERE id = ? AND compose_id = ?
		`, in.Host, in.Path, in.InternalPath, boolInt(in.StripPath), in.Service, in.Port, in.Entrypoint, boolInt(in.HTTPS), in.CertProvider, in.CertResolver, string(middlewares), at, id, composeID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	}, at)
	if err != nil {
		return Domain{}, s.explain(ctx, err, in)
	}
	return s.get(ctx, composeID, id)
}

func (s *Store) delete(ctx context.Context, composeID, id int64) error {
	return s.inTx(ctx, composeID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM domains WHERE id = ? AND compose_id = ?`, id, composeID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	}, s.now().Unix())
}

func (s *Store) get(ctx context.Context, composeID, id int64) (Domain, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM domains WHERE id = ? AND compose_id = ?`, id, composeID)
	d, err := scanDomain(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	return d, err
}

// inTx runs write and stamps the Compose's domains_changed_at in one
// transaction, so a change and its redeploy marker land together.
func (s *Store) inTx(ctx context.Context, composeID int64, write func(*sql.Tx) error, at int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE composes SET domains_changed_at = ? WHERE id = ?`, at, composeID); err != nil {
		return err
	}
	return tx.Commit()
}

func requireAffected(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// explain turns a UNIQUE (host, path) failure into a ConflictError naming the owner.
func (s *Store) explain(ctx context.Context, err error, in Input) error {
	if !strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return err
	}
	var project, environment, compose string
	lookup := s.db.QueryRowContext(ctx, `
		SELECT p.name, e.name, c.name
		FROM domains d
		JOIN composes c ON c.id = d.compose_id
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE d.host = ? AND d.path = ?
	`, in.Host, in.Path).Scan(&project, &environment, &compose)
	if lookup != nil {
		return err
	}
	address := Domain{Host: in.Host, Path: in.Path}.Address()
	return &ConflictError{Message: address + " is already used by " + strings.Join([]string{project, environment, compose}, " / ")}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Service serves the domains HTTP routes.
type Service struct {
	store *Store
}

// New returns the domains HTTP service over the migrated database.
func New(db *sql.DB) *Service {
	return &Service{store: NewStore(db)}
}

// RegisterRoutes adds the authenticated domains endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/domains"
	mux.HandleFunc("GET "+base, s.list)
	mux.HandleFunc("POST "+base, s.create)
	mux.HandleFunc("PUT "+base+"/{domainID}", s.update)
	mux.HandleFunc("DELETE "+base+"/{domainID}", s.delete)
}

// route holds the IDs a request's path names.
type route struct {
	projectID, environmentID, composeID, domainID int64
}

func parseRoute(w http.ResponseWriter, r *http.Request, withDomain bool) (route, bool) {
	names := []string{"projectID", "environmentID", "composeID"}
	if withDomain {
		names = append(names, "domainID")
	}
	ids := make([]int64, 4)
	for i, name := range names {
		id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, strings.TrimSuffix(name, "ID")+" id must be a positive integer")
			return route{}, false
		}
		ids[i] = id
	}
	return route{ids[0], ids[1], ids[2], ids[3]}, true
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(w, r, false)
	if !ok {
		return
	}
	yamlContent, err := s.store.composeYAML(r.Context(), rt.projectID, rt.environmentID, rt.composeID)
	if err != nil {
		writeStoreError(w, err, "could not load compose")
		return
	}
	list, err := s.store.ListForCompose(r.Context(), rt.composeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load domains")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": list, "services": DeclaredServices(yamlContent)})
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(w, r, false)
	if !ok {
		return
	}
	in, ok := s.readInput(w, r, rt)
	if !ok {
		return
	}
	d, err := s.store.create(r.Context(), rt.composeID, in)
	if err != nil {
		writeStoreError(w, err, "could not save domain")
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(w, r, true)
	if !ok {
		return
	}
	in, ok := s.readInput(w, r, rt)
	if !ok {
		return
	}
	d, err := s.store.update(r.Context(), rt.composeID, rt.domainID, in)
	if err != nil {
		writeStoreError(w, err, "could not save domain")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(w, r, true)
	if !ok {
		return
	}
	if _, err := s.store.composeYAML(r.Context(), rt.projectID, rt.environmentID, rt.composeID); err != nil {
		writeStoreError(w, err, "could not load compose")
		return
	}
	if err := s.store.delete(r.Context(), rt.composeID, rt.domainID); err != nil {
		writeStoreError(w, err, "could not delete domain")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readInput checks the Compose exists, decodes the body and validates it
// against the Compose's saved Services; it writes the error response itself.
func (s *Service) readInput(w http.ResponseWriter, r *http.Request, rt route) (Input, bool) {
	yamlContent, err := s.store.composeYAML(r.Context(), rt.projectID, rt.environmentID, rt.composeID)
	if err != nil {
		writeStoreError(w, err, "could not load compose")
		return Input{}, false
	}
	var in Input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err := decoder.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "request must contain a valid JSON object")
		return Input{}, false
	}
	in, fieldErr := Validate(in, DeclaredServices(yamlContent))
	if fieldErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fieldErr.Message, "field": fieldErr.Field})
		return Input{}, false
	}
	return in, true
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	var conflict *ConflictError
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, conflict.Message)
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
