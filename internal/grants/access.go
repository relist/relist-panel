// Package grants owns the grants table: the Role a User holds on a Project
// or one of its Environments, and the effective access that follows from it
// (.scratch/multi-user/spec.md, "Effective role"). authz reads Access on
// every request; handlers read it from the request context to filter lists
// and strip secret values.
package grants

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Role is what a User may do in an Environment, in increasing order.
type Role int

const (
	RoleNone Role = iota
	RoleViewer
	RoleOperator
	RoleEditor
)

var roleNames = [...]string{"none", "viewer", "operator", "editor"}

func (r Role) String() string {
	if r < RoleNone || r > RoleEditor {
		return fmt.Sprintf("Role(%d)", int(r))
	}
	return roleNames[r]
}

// ParseRole reads a stored or requested role name.
func ParseRole(name string) (Role, bool) {
	for i, n := range roleNames {
		if n == name {
			return Role(i), true
		}
	}
	return RoleNone, false
}

func (r Role) MarshalJSON() ([]byte, error) { return json.Marshal(r.String()) }

// Querier is the read side Load needs: a *sql.DB or a *sql.Tx.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Access is what one User may reach. An admin's Access has Admin set and
// nothing else: it reaches everything.
type Access struct {
	Admin bool

	// projectGrant is the Project Grant per Project, when there is one.
	projectGrant map[int64]Role
	// envRole is the effective role of every Environment of a Project the
	// User holds any Grant in; an absent Environment has none.
	envRole map[int64]Role
	// projectEnvs lists those Projects' Environments, ascending.
	projectEnvs map[int64][]int64
}

// Load reads userID's Grants and works out the effective role of every
// Environment they touch. An admin needs no query.
func Load(ctx context.Context, q Querier, userID int64, isAdmin bool) (Access, error) {
	if isAdmin {
		return Access{Admin: true}, nil
	}
	access := Access{projectGrant: map[int64]Role{}, envRole: map[int64]Role{}, projectEnvs: map[int64][]int64{}}
	envGrant := map[int64]Role{}
	rows, err := q.QueryContext(ctx, "SELECT project_id, environment_id, role FROM grants WHERE user_id = ?", userID)
	if err != nil {
		return Access{}, err
	}
	projects := map[int64]bool{}
	for rows.Next() {
		var (
			projectID int64
			envID     sql.NullInt64
			name      string
		)
		if err := rows.Scan(&projectID, &envID, &name); err != nil {
			rows.Close()
			return Access{}, err
		}
		role, ok := ParseRole(name)
		if !ok {
			rows.Close()
			return Access{}, fmt.Errorf("grant of user %d has unknown role %q", userID, name)
		}
		projects[projectID] = true
		if envID.Valid {
			envGrant[envID.Int64] = role
		} else {
			access.projectGrant[projectID] = role
		}
	}
	if err := rows.Close(); err != nil {
		return Access{}, err
	}
	if err := rows.Err(); err != nil {
		return Access{}, err
	}
	if len(projects) == 0 {
		return access, nil
	}
	ids := make([]any, 0, len(projects))
	for id := range projects {
		ids = append(ids, id)
		access.projectEnvs[id] = nil
	}
	rows, err = q.QueryContext(ctx,
		"SELECT id, project_id FROM environments WHERE project_id IN (?"+strings.Repeat(", ?", len(ids)-1)+") ORDER BY id", ids...)
	if err != nil {
		return Access{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var envID, projectID int64
		if err := rows.Scan(&envID, &projectID); err != nil {
			return Access{}, err
		}
		access.projectEnvs[projectID] = append(access.projectEnvs[projectID], envID)
		role, ok := envGrant[envID]
		if !ok {
			role = access.projectGrant[projectID]
		}
		if role != RoleNone {
			access.envRole[envID] = role
		}
	}
	return access, rows.Err()
}

// EnvironmentRole is the User's effective role in an Environment. An admin
// is an Editor everywhere.
func (a Access) EnvironmentRole(environmentID int64) Role {
	if a.Admin {
		return RoleEditor
	}
	return a.envRole[environmentID]
}

// ProjectGrant is the Project Grant alone, which makes a Project Editor.
func (a Access) ProjectGrant(projectID int64) Role {
	if a.Admin {
		return RoleEditor
	}
	return a.projectGrant[projectID]
}

// ProjectVisible reports whether the Project shows at all: one of its
// Environments has a role, or it has none and the User holds a Project Grant.
func (a Access) ProjectVisible(projectID int64) bool {
	if a.Admin {
		return true
	}
	envs, touched := a.projectEnvs[projectID]
	if !touched {
		return false
	}
	if len(envs) == 0 {
		return a.projectGrant[projectID] != RoleNone
	}
	for _, env := range envs {
		if a.envRole[env] != RoleNone {
			return true
		}
	}
	return false
}

// VisibleEnvironments lists the Project's Environments the User has a role
// in, ascending. For an admin it is nil: every Environment is visible.
func (a Access) VisibleEnvironments(projectID int64) []int64 {
	var out []int64
	for _, env := range a.projectEnvs[projectID] {
		if a.envRole[env] != RoleNone {
			out = append(out, env)
		}
	}
	return out
}

// AllVisibleEnvironments lists every Environment the User has a role in,
// ascending; nil for an admin, who sees all.
func (a Access) AllVisibleEnvironments() []int64 {
	if a.Admin {
		return nil
	}
	out := make([]int64, 0, len(a.envRole))
	for env := range a.envRole {
		out = append(out, env)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// AccessView is the wire form of a member's Access for GET /api/auth/status.
type AccessView struct {
	Projects []ProjectAccess `json:"projects"`
}

// ProjectAccess is one visible Project: its Project Grant (null when there is
// none) and the Environments with a role.
type ProjectAccess struct {
	ID           int64               `json:"id"`
	Role         *Role               `json:"role"`
	Environments []EnvironmentAccess `json:"environments"`
}

// EnvironmentAccess is one visible Environment's effective role.
type EnvironmentAccess struct {
	ID   int64 `json:"id"`
	Role Role  `json:"role"`
}

// View is nil for an admin, who sees everything.
func (a Access) View() *AccessView {
	if a.Admin {
		return nil
	}
	view := &AccessView{Projects: []ProjectAccess{}}
	ids := make([]int64, 0, len(a.projectEnvs))
	for id := range a.projectEnvs {
		if a.ProjectVisible(id) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		p := ProjectAccess{ID: id, Environments: []EnvironmentAccess{}}
		if role, ok := a.projectGrant[id]; ok {
			p.Role = &role
		}
		for _, env := range a.VisibleEnvironments(id) {
			p.Environments = append(p.Environments, EnvironmentAccess{ID: env, Role: a.envRole[env]})
		}
		view.Projects = append(view.Projects, p)
	}
	return view
}

var errInvalidRole = errors.New("role must be none, viewer, operator or editor")
