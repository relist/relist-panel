// Package variablesets owns Variable Sets, their entries and their bindings to
// Services, and the HTTP API for them. A Variable Set is a named list of
// key/value pairs in one Compose that reaches only the containers of the
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

	"relist-panel/internal/authz"
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

// Set is the API representation of a Variable Set.
type Set struct {
	ID            int64     `json:"id"`
	ComposeID     int64     `json:"composeId"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Entries       []Entry   `json:"entries"`
	BoundServices []string  `json:"boundServices"`
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
	const compose = environment + "/composes/{composeID}"
	const sets = compose + "/variable-sets"
	mux.HandleFunc("GET "+sets, s.list)
	mux.HandleFunc("POST "+sets, s.create)
	mux.HandleFunc("PUT "+sets+"/{setID}", s.rename)
	mux.HandleFunc("DELETE "+sets+"/{setID}", s.delete)
	mux.HandleFunc("POST "+sets+"/{setID}/entries", s.createEntry)
	mux.HandleFunc("PUT "+sets+"/{setID}/entries/{entryID}", s.updateEntry)
	mux.HandleFunc("DELETE "+sets+"/{setID}/entries/{entryID}", s.deleteEntry)
	mux.HandleFunc("GET "+compose+"/bindings", s.listBindings)
	mux.HandleFunc("PUT "+compose+"/bindings/{service}", s.replaceBindings)
}

var setName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func validateName(name string) error {
	if !setName.MatchString(name) {
		return errors.New("set name must be 1-63 characters: lowercase letters, digits and -, starting with a letter")
	}
	return nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	sets, err := s.loadSets(r.Context(), composeID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load variable sets")
		return
	}
	if !authz.SecretsVisible(r) {
		for i := range sets {
			for j := range sets[i].Entries {
				if sets[i].Entries[j].Secret {
					sets[i].Entries[j].Value = ""
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, sets)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	now := s.now().Unix()
	result, err := s.db.ExecContext(r.Context(), `
		INSERT INTO variable_sets (compose_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, composeID, name, now, now)
	if err != nil {
		writeWriteError(w, err, "could not create variable set")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create variable set")
		return
	}
	s.respond(w, r, http.StatusCreated, composeID, id)
}

func (s *Service) rename(w http.ResponseWriter, r *http.Request) {
	composeID, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	// A rename does not stamp variables_changed_at: set files are named by ID,
	// so nothing deployed changes.
	err := func() error {
		result, err := s.db.ExecContext(r.Context(), `
			UPDATE variable_sets SET name = ?, updated_at = ? WHERE compose_id = ? AND id = ?
		`, name, s.now().Unix(), composeID, setID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	}()
	if err != nil {
		writeWriteError(w, err, "could not rename variable set")
		return
	}
	s.respond(w, r, http.StatusOK, composeID, setID)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	composeID, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	// Deleting cascades the set's entries and bindings. When it had bindings the
	// next deploy changes, so the Compose is stamped in the same transaction.
	err := func() error {
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var bindings int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM service_bindings WHERE set_id = ?`, setID).Scan(&bindings); err != nil {
			return err
		}
		result, err := tx.ExecContext(r.Context(), `DELETE FROM variable_sets WHERE compose_id = ? AND id = ?`, composeID, setID)
		if err != nil {
			return err
		}
		if err := requireAffected(result); err != nil {
			return err
		}
		if bindings > 0 {
			if _, err := tx.ExecContext(r.Context(), `UPDATE composes SET variables_changed_at = ? WHERE id = ?`, s.now().Unix(), composeID); err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		writeWriteError(w, err, "could not delete variable set")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// respond reads the stored set back and writes it.
func (s *Service) respond(w http.ResponseWriter, r *http.Request, status int, composeID, setID int64) {
	sets, err := s.loadSets(r.Context(), composeID, setID)
	if err != nil || len(sets) != 1 {
		writeError(w, http.StatusInternalServerError, "could not load variable set")
		return
	}
	writeJSON(w, status, sets[0])
}

// loadSets reads the Compose's sets with their entries and bound Services, or
// only set setID when it is non-zero.
func (s *Service) loadSets(ctx context.Context, composeID, setID int64) ([]Set, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, compose_id, name, created_at, updated_at
		FROM variable_sets
		WHERE compose_id = ? AND (? = 0 OR id = ?)
		ORDER BY name ASC
	`, composeID, setID, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sets := make([]Set, 0)
	index := map[int64]int{}
	for rows.Next() {
		var set Set
		var createdAt, updatedAt int64
		if err := rows.Scan(&set.ID, &set.ComposeID, &set.Name, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		set.CreatedAt, set.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
		set.Entries, set.BoundServices = []Entry{}, []string{}
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
		WHERE s.compose_id = ? AND (? = 0 OR s.id = ?)
		ORDER BY e.id ASC
	`, composeID, setID, setID)
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

	bound, err := s.db.QueryContext(ctx, `
		SELECT b.set_id, b.service
		FROM service_bindings b
		JOIN variable_sets s ON s.id = b.set_id
		WHERE s.compose_id = ? AND (? = 0 OR s.id = ?)
		ORDER BY b.service ASC
	`, composeID, setID, setID)
	if err != nil {
		return nil, err
	}
	defer bound.Close()
	for bound.Next() {
		var id int64
		var service string
		if err := bound.Scan(&id, &service); err != nil {
			return nil, err
		}
		sets[index[id]].BoundServices = append(sets[index[id]].BoundServices, service)
	}
	return sets, bound.Err()
}

// inTx runs write, then bumps the set's updated_at and, when the set is bound
// to at least one Service, stamps its Compose's variables_changed_at, all in
// one transaction, so a change and its redeploy marker land together.
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
		WHERE id = (SELECT compose_id FROM variable_sets WHERE id = ?)
		  AND EXISTS (SELECT 1 FROM service_bindings WHERE set_id = ?)
	`, now, setID, setID); err != nil {
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
