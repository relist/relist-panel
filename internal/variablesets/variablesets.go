// Package variablesets owns Variable Sets, their entries and their bindings to
// Services, and the HTTP API for them. A Variable Set is a named list of
// key/value pairs in one Environment that reaches only the containers of the
// Services it is bound to (ADR-0015). The deploy-time files are written by
// internal/composes.
package variablesets

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Entry is one key/value pair of a set. It has the Environment Variable wire
// shape, keyed to a set.
type Entry struct {
	ID     int64  `json:"id"`
	SetID  int64  `json:"setId"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// Binder is a Service of a Compose a set is bound to.
type Binder struct {
	ComposeID   int64  `json:"composeId"`
	ComposeName string `json:"composeName"`
	Service     string `json:"service"`
}

// Set is the API representation of a Variable Set.
type Set struct {
	ID            int64     `json:"id"`
	EnvironmentID int64     `json:"environmentId"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Entries       []Entry   `json:"entries"`
	UsedBy        []Binder  `json:"usedBy"`
}

type setInput struct {
	Name string `json:"name"`
}

// Service serves the Variable Set endpoints.
type Service struct {
	db  *sql.DB
	now func() time.Time
}

// New constructs the service over the application's migrated SQLite database.
func New(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// RegisterRoutes adds the authenticated Variable Set endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const environment = "/api/projects/{projectID}/environments/{environmentID}"
	const sets = environment + "/variable-sets"
	mux.HandleFunc("GET "+sets, s.list)
	mux.HandleFunc("POST "+sets, s.create)
	mux.HandleFunc("PUT "+sets+"/{setID}", s.rename)
	mux.HandleFunc("DELETE "+sets+"/{setID}", s.delete)
	mux.HandleFunc("POST "+sets+"/{setID}/entries", s.createEntry)
	mux.HandleFunc("PUT "+sets+"/{setID}/entries/{entryID}", s.updateEntry)
	mux.HandleFunc("DELETE "+sets+"/{setID}/entries/{entryID}", s.deleteEntry)
	mux.HandleFunc("GET "+environment+"/composes/{composeID}/bindings", s.listBindings)
	mux.HandleFunc("PUT "+environment+"/composes/{composeID}/bindings/{service}", s.replaceBindings)
}

var setName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func validateName(name string) error {
	if !setName.MatchString(name) {
		return errors.New("set name must be 1-63 characters: lowercase letters, digits and -, starting with a letter")
	}
	return nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := s.requireEnvironment(w, r)
	if !ok {
		return
	}
	sets, err := s.loadSets(r.Context(), environmentID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load variable sets")
		return
	}
	writeJSON(w, http.StatusOK, sets)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	environmentID, ok := s.requireEnvironment(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	now := s.now().Unix()
	result, err := s.db.ExecContext(r.Context(), `
		INSERT INTO variable_sets (environment_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, environmentID, name, now, now)
	if err != nil {
		writeWriteError(w, err, "could not create variable set")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create variable set")
		return
	}
	s.respond(w, r, http.StatusCreated, environmentID, id)
}

func (s *Service) rename(w http.ResponseWriter, r *http.Request) {
	environmentID, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	err := s.inTx(r.Context(), setID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `
			UPDATE variable_sets SET name = ? WHERE environment_id = ? AND id = ?
		`, name, environmentID, setID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	})
	if err != nil {
		writeWriteError(w, err, "could not rename variable set")
		return
	}
	s.respond(w, r, http.StatusOK, environmentID, setID)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	environmentID, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	var binders []Binder
	err := func() error {
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if binders, err = loadBinders(r.Context(), tx, setID); err != nil {
			return err
		}
		if len(binders) > 0 {
			return nil
		}
		result, err := tx.ExecContext(r.Context(), `DELETE FROM variable_sets WHERE environment_id = ? AND id = ?`, environmentID, setID)
		if err != nil {
			return err
		}
		if err := requireAffected(result); err != nil {
			return err
		}
		return tx.Commit()
	}()
	switch {
	case err != nil:
		writeWriteError(w, err, "could not delete variable set")
	case len(binders) > 0:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "variable set is still bound to services; unbind it first",
			"usedBy": binders,
		})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// respond reads the stored set back and writes it.
func (s *Service) respond(w http.ResponseWriter, r *http.Request, status int, environmentID, setID int64) {
	sets, err := s.loadSets(r.Context(), environmentID, setID)
	if err != nil || len(sets) != 1 {
		writeError(w, http.StatusInternalServerError, "could not load variable set")
		return
	}
	writeJSON(w, status, sets[0])
}

// loadSets reads the Environment's sets with their entries and binders, or
// only set setID when it is non-zero.
func (s *Service) loadSets(ctx context.Context, environmentID, setID int64) ([]Set, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, environment_id, name, created_at, updated_at
		FROM variable_sets
		WHERE environment_id = ? AND (? = 0 OR id = ?)
		ORDER BY name ASC
	`, environmentID, setID, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sets := make([]Set, 0)
	index := map[int64]int{}
	for rows.Next() {
		var set Set
		var createdAt, updatedAt int64
		if err := rows.Scan(&set.ID, &set.EnvironmentID, &set.Name, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		set.CreatedAt, set.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		set.Entries, set.UsedBy = []Entry{}, []Binder{}
		index[set.ID] = len(sets)
		sets = append(sets, set)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	entries, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.set_id, e.key, e.value, e.secret
		FROM variable_set_entries e
		JOIN variable_sets s ON s.id = e.set_id
		WHERE s.environment_id = ? AND (? = 0 OR s.id = ?)
		ORDER BY e.id ASC
	`, environmentID, setID, setID)
	if err != nil {
		return nil, err
	}
	defer entries.Close()
	for entries.Next() {
		var entry Entry
		if err := entries.Scan(&entry.ID, &entry.SetID, &entry.Key, &entry.Value, &entry.Secret); err != nil {
			return nil, err
		}
		sets[index[entry.SetID]].Entries = append(sets[index[entry.SetID]].Entries, entry)
	}
	if err := entries.Err(); err != nil {
		return nil, err
	}

	binders, err := s.db.QueryContext(ctx, `
		SELECT b.set_id, c.id, c.name, b.service
		FROM service_bindings b
		JOIN composes c ON c.id = b.compose_id
		JOIN variable_sets s ON s.id = b.set_id
		WHERE s.environment_id = ? AND (? = 0 OR s.id = ?)
		ORDER BY c.name ASC, b.service ASC
	`, environmentID, setID, setID)
	if err != nil {
		return nil, err
	}
	defer binders.Close()
	for binders.Next() {
		var binder Binder
		var id int64
		if err := binders.Scan(&id, &binder.ComposeID, &binder.ComposeName, &binder.Service); err != nil {
			return nil, err
		}
		sets[index[id]].UsedBy = append(sets[index[id]].UsedBy, binder)
	}
	return sets, binders.Err()
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// loadBinders lists every Compose and Service a set is bound to.
func loadBinders(ctx context.Context, q queryer, setID int64) ([]Binder, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT c.id, c.name, b.service
		FROM service_bindings b
		JOIN composes c ON c.id = b.compose_id
		WHERE b.set_id = ?
		ORDER BY c.name ASC, b.service ASC
	`, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var binders []Binder
	for rows.Next() {
		var binder Binder
		if err := rows.Scan(&binder.ComposeID, &binder.ComposeName, &binder.Service); err != nil {
			return nil, err
		}
		binders = append(binders, binder)
	}
	return binders, rows.Err()
}

// inTx runs write, then stamps variables_changed_at on every Compose that
// binds the set and bumps the set's updated_at, all in one transaction, so a
// change and its redeploy marker land together.
func (s *Service) inTx(ctx context.Context, setID int64, write func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(tx); err != nil {
		return err
	}
	now := s.now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE variable_sets SET updated_at = ? WHERE id = ?`, now, setID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE composes SET variables_changed_at = ?
		WHERE id IN (SELECT compose_id FROM service_bindings WHERE set_id = ?)
	`, now, setID); err != nil {
		return err
	}
	return tx.Commit()
}

func writeWriteError(w http.ResponseWriter, err error, message string) {
	var invalid invalidInput
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "variable set not found")
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Error())
	case strings.Contains(strings.ToLower(err.Error()), "unique constraint"):
		writeError(w, http.StatusConflict, "that name or key already exists")
	default:
		writeError(w, http.StatusInternalServerError, message)
	}
}
