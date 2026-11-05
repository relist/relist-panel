package grants

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Grant is one row of the grants table as the Grants routes show it. A nil
// EnvironmentID is a Project Grant.
type Grant struct {
	UserID        int64  `json:"userId"`
	Email         string `json:"email"`
	ProjectID     int64  `json:"projectId"`
	EnvironmentID *int64 `json:"environmentId"`
	Role          Role   `json:"role"`
}

// errNotFound is an unknown User or Project in the path.
var errNotFound = errors.New("not found")

// validationError is a 400 with its message.
type validationError struct{ message string }

func (e validationError) Error() string { return e.message }

func invalid(format string, args ...any) error {
	return validationError{fmt.Sprintf(format, args...)}
}

const grantSelect = `SELECT grants.user_id, users.email, grants.project_id, grants.environment_id, grants.role
	FROM grants JOIN users ON users.id = grants.user_id`

const grantOrder = ` ORDER BY grants.user_id, grants.project_id, grants.environment_id IS NOT NULL, grants.environment_id`

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listGrants(ctx context.Context, q queryer, where string, arg int64) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, grantSelect+" WHERE "+where+grantOrder, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var (
			g    Grant
			env  sql.NullInt64
			name string
		)
		if err := rows.Scan(&g.UserID, &g.Email, &g.ProjectID, &env, &name); err != nil {
			return nil, err
		}
		if env.Valid {
			id := env.Int64
			g.EnvironmentID = &id
		}
		role, ok := ParseRole(name)
		if !ok {
			return nil, fmt.Errorf("grant has unknown role %q", name)
		}
		g.Role = role
		out = append(out, g)
	}
	return out, rows.Err()
}

func exists(ctx context.Context, q queryer, table string, id int64) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// grantInput is one requested Grant; the route fixes either the User or the
// Project, and the other comes from the body.
type grantInput struct {
	UserID        int64  `json:"userId"`
	ProjectID     int64  `json:"projectId"`
	EnvironmentID *int64 `json:"environmentId"`
	Role          string `json:"role"`
}

// replace deletes the Grants that scope (user_id = ? or project_id = ?)
// selects and writes inputs in their place, in one transaction, after
// validating every input.
func replace(ctx context.Context, db *sql.DB, scope string, scopeID int64, inputs []grantInput) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type target struct{ user, project, env int64 }
	seen := map[target]bool{}
	for _, in := range inputs {
		role, ok := ParseRole(in.Role)
		if !ok {
			return validationError{errInvalidRole.Error()}
		}
		if role == RoleNone && in.EnvironmentID == nil {
			return invalid("No access is only for an Environment")
		}
		if ok, err := exists(ctx, tx, "users", in.UserID); err != nil {
			return err
		} else if !ok {
			return invalid("user %d not found", in.UserID)
		}
		if ok, err := exists(ctx, tx, "projects", in.ProjectID); err != nil {
			return err
		} else if !ok {
			return invalid("project %d not found", in.ProjectID)
		}
		key := target{user: in.UserID, project: in.ProjectID}
		if in.EnvironmentID != nil {
			var projectID int64
			err := tx.QueryRowContext(ctx, "SELECT project_id FROM environments WHERE id = ?", *in.EnvironmentID).Scan(&projectID)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && projectID != in.ProjectID) {
				return invalid("environment %d does not belong to project %d", *in.EnvironmentID, in.ProjectID)
			} else if err != nil {
				return err
			}
			key = target{user: in.UserID, env: *in.EnvironmentID}
		}
		if seen[key] {
			return invalid("grant for the same target listed twice")
		}
		seen[key] = true
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM grants WHERE "+scope+" = ?", scopeID); err != nil {
		return err
	}
	for _, in := range inputs {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (?, ?, ?, ?)",
			in.UserID, in.ProjectID, in.EnvironmentID, in.Role,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
