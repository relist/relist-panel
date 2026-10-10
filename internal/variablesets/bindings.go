package variablesets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"
)

const maxServiceName = 255

// ServiceBindings is the Variable Sets bound to one Service, in binding order.
type ServiceBindings struct {
	Service string  `json:"service"`
	SetIDs  []int64 `json:"setIds"`
}

type bindingsInput struct {
	SetIDs *[]int64 `json:"setIds"`
}

// listBindings answers every binding of the Compose, grouped by Service. The
// Service need not exist in the saved YAML, so stale bindings stay visible.
func (s *Service) listBindings(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT service, set_id FROM service_bindings WHERE compose_id = ? ORDER BY service ASC, position ASC
	`, composeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load bindings")
		return
	}
	defer rows.Close()
	groups := make([]ServiceBindings, 0)
	for rows.Next() {
		var service string
		var setID int64
		if err := rows.Scan(&service, &setID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not load bindings")
			return
		}
		if n := len(groups); n == 0 || groups[n-1].Service != service {
			groups = append(groups, ServiceBindings{Service: service, SetIDs: []int64{}})
		}
		last := &groups[len(groups)-1]
		last.SetIDs = append(last.SetIDs, setID)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not load bindings")
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// replaceBindings makes one Service's bindings exactly the given sets, in the
// given order. An empty list removes them all.
func (s *Service) replaceBindings(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	service := r.PathValue("service")
	if service == "" || utf8.RuneCountInString(service) > maxServiceName {
		writeError(w, http.StatusBadRequest, "service must be 1-255 characters")
		return
	}
	var in bindingsInput
	if !decodeBody(w, r, &in) {
		return
	}
	if in.SetIDs == nil {
		writeError(w, http.StatusBadRequest, "setIds is required")
		return
	}
	setIDs := *in.SetIDs

	err := func() error {
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := checkSets(r.Context(), tx, composeID, setIDs); err != nil {
			return err
		}
		current, err := currentSetIDs(r.Context(), tx, composeID, service)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM service_bindings WHERE compose_id = ? AND service = ?`, composeID, service); err != nil {
			return err
		}
		for position, setID := range setIDs {
			if _, err := tx.ExecContext(r.Context(), `
				INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (?, ?, ?, ?)
			`, composeID, service, setID, position); err != nil {
				return err
			}
		}
		if !equal(current, setIDs) {
			if _, err := tx.ExecContext(r.Context(), `UPDATE composes SET variables_changed_at = ? WHERE id = ?`, s.now().Unix(), composeID); err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	if err != nil {
		writeWriteError(w, err, "could not save bindings")
		return
	}
	if setIDs == nil {
		setIDs = []int64{}
	}
	writeJSON(w, http.StatusOK, ServiceBindings{Service: service, SetIDs: setIDs})
}

// checkSets refuses duplicates and sets that are missing or belong to another
// Compose.
func checkSets(ctx context.Context, tx *sql.Tx, composeID int64, setIDs []int64) error {
	seen := make(map[int64]bool, len(setIDs))
	for _, setID := range setIDs {
		if seen[setID] {
			return invalidInput(fmt.Sprintf("variable set %d is listed twice", setID))
		}
		seen[setID] = true
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM variable_sets WHERE id = ? AND compose_id = ?`, setID, composeID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return invalidInput(fmt.Sprintf("variable set %d does not exist in this compose", setID))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func currentSetIDs(ctx context.Context, tx *sql.Tx, composeID int64, service string) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT set_id FROM service_bindings WHERE compose_id = ? AND service = ? ORDER BY position ASC`, composeID, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
