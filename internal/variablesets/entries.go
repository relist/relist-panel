package variablesets

import (
	"database/sql"
	"net/http"
	"relist-panel/internal/audit"

	"relist-panel/internal/environmentvariables"
)

type entryInput struct {
	Key    string  `json:"key"`
	Value  *string `json:"value"`
	Secret bool    `json:"secret"`
}

func (s *Service) createEntry(w http.ResponseWriter, r *http.Request) {
	composeID, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	in, key, value, ok := decodeEntry(w, r)
	if !ok {
		return
	}
	audit.Detail(r.Context(), "key", key)
	var id int64
	err := s.inTx(r.Context(), setID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `
			INSERT INTO variable_set_entries (set_id, key, value, secret) VALUES (?, ?, ?, ?)
		`, setID, key, value, in.Secret)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	if err != nil {
		writeWriteError(w, err, "could not create entry")
		return
	}
	s.respondEntry(w, r, http.StatusCreated, composeID, setID, id)
}

func (s *Service) updateEntry(w http.ResponseWriter, r *http.Request) {
	composeID, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	entryID, ok := entryID(w, r)
	if !ok {
		return
	}
	in, key, value, ok := decodeEntry(w, r)
	if !ok {
		return
	}
	audit.Detail(r.Context(), "key", key)
	err := s.inTx(r.Context(), setID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `
			UPDATE variable_set_entries SET key = ?, value = ?, secret = ? WHERE set_id = ? AND id = ?
		`, key, value, in.Secret, setID, entryID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	})
	if err != nil {
		writeWriteError(w, err, "could not update entry")
		return
	}
	s.respondEntry(w, r, http.StatusOK, composeID, setID, entryID)
}

func (s *Service) deleteEntry(w http.ResponseWriter, r *http.Request) {
	_, setID, ok := s.requireSet(w, r)
	if !ok {
		return
	}
	entryID, ok := entryID(w, r)
	if !ok {
		return
	}
	var key string
	if err := s.db.QueryRowContext(r.Context(), "SELECT key FROM variable_set_entries WHERE set_id = ? AND id = ?", setID, entryID).Scan(&key); err == nil {
		audit.Detail(r.Context(), "key", key)
	}
	err := s.inTx(r.Context(), setID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `DELETE FROM variable_set_entries WHERE set_id = ? AND id = ?`, setID, entryID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	})
	if err != nil {
		writeWriteError(w, err, "could not delete entry")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) respondEntry(w http.ResponseWriter, r *http.Request, status int, composeID, setID, id int64) {
	sets, err := s.loadSets(r.Context(), composeID, setID)
	if err != nil || len(sets) != 1 {
		writeError(w, http.StatusInternalServerError, "could not load entry")
		return
	}
	for _, entry := range sets[0].Entries {
		if entry.ID == id {
			writeJSON(w, status, entry)
			return
		}
	}
	writeError(w, http.StatusInternalServerError, "could not load entry")
}

func entryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parsePositiveID(r.PathValue("entryID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "entry id must be a positive integer")
		return 0, false
	}
	return id, true
}

func decodeEntry(w http.ResponseWriter, r *http.Request) (entryInput, string, string, bool) {
	var in entryInput
	if !decodeBody(w, r, &in) {
		return entryInput{}, "", "", false
	}
	key, value, err := environmentvariables.ValidateKeyValue("variable", in.Key, in.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return entryInput{}, "", "", false
	}
	return in, key, value, true
}
