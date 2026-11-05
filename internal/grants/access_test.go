package grants

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"relist-panel/internal/database"
)

// Seeded ids: Project P (1) with Environments E1 (11) and E2 (12), an empty
// Project Q (2), an untouched Project R (3) with Environment R1 (31); User 2.
const (
	pP, pQ, pR = 1, 2, 3
	e1, e2, r1 = 11, 12, 31
	member     = 2
)

func seed(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES (1, 'owner@example.com', 'x', 1, 1), (2, 'member@example.com', 'x', 0, 1), (3, 'other@example.com', 'x', 0, 1);
		INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'p', '', 1, 1), (2, 'q', '', 1, 1), (3, 'r', '', 1, 1);
		INSERT INTO environments (id, project_id, name, created_at) VALUES (11, 1, 'one', 1), (12, 1, 'two', 1), (31, 3, 'r-one', 1);
	`); err != nil {
		t.Fatal(err)
	}
	return db
}

type row struct {
	project     int64
	environment int64 // 0 = Project Grant
	role        string
}

func give(t *testing.T, db *sql.DB, userID int64, rows ...row) {
	t.Helper()
	for _, g := range rows {
		var env any
		if g.environment != 0 {
			env = g.environment
		}
		if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (?, ?, ?, ?)", userID, g.project, env, g.role); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEffectiveAccess(t *testing.T) {
	type expect struct {
		roles   map[int64]Role // environment → role
		visible map[int64]bool // project → visible
		grant   map[int64]Role // project → Project Grant
		envs    map[int64][]int64
	}
	cases := []struct {
		name   string
		grants []row
		admin  bool
		want   expect
	}{
		{
			name: "no grant",
			want: expect{
				roles:   map[int64]Role{e1: RoleNone, e2: RoleNone, r1: RoleNone},
				visible: map[int64]bool{pP: false, pQ: false, pR: false},
			},
		},
		{
			name:   "project viewer",
			grants: []row{{pP, 0, "viewer"}},
			want: expect{
				roles:   map[int64]Role{e1: RoleViewer, e2: RoleViewer, r1: RoleNone},
				visible: map[int64]bool{pP: true, pQ: false, pR: false},
				grant:   map[int64]Role{pP: RoleViewer},
			},
		},
		{
			name:   "environment raises the project",
			grants: []row{{pP, 0, "viewer"}, {pP, e2, "editor"}},
			want:   expect{roles: map[int64]Role{e1: RoleViewer, e2: RoleEditor}},
		},
		{
			name:   "no access hides one environment",
			grants: []row{{pP, 0, "editor"}, {pP, e1, "none"}},
			want: expect{
				roles:   map[int64]Role{e1: RoleNone, e2: RoleEditor},
				visible: map[int64]bool{pP: true},
				grant:   map[int64]Role{pP: RoleEditor},
				envs:    map[int64][]int64{pP: {e2}},
			},
		},
		{
			name:   "no access on every environment hides the project",
			grants: []row{{pP, 0, "viewer"}, {pP, e1, "none"}, {pP, e2, "none"}},
			want:   expect{visible: map[int64]bool{pP: false}, envs: map[int64][]int64{pP: nil}},
		},
		{
			name:   "environment grant alone",
			grants: []row{{pP, e1, "operator"}},
			want: expect{
				roles:   map[int64]Role{e1: RoleOperator, e2: RoleNone},
				visible: map[int64]bool{pP: true},
				grant:   map[int64]Role{pP: RoleNone},
				envs:    map[int64][]int64{pP: {e1}},
			},
		},
		{
			name:   "project with no environments",
			grants: []row{{pQ, 0, "viewer"}},
			want:   expect{visible: map[int64]bool{pQ: true, pP: false}, envs: map[int64][]int64{pQ: nil}},
		},
		{
			name:  "admin",
			admin: true,
			want: expect{
				roles:   map[int64]Role{e1: RoleEditor, e2: RoleEditor, r1: RoleEditor},
				visible: map[int64]bool{pP: true, pQ: true, pR: true},
				grant:   map[int64]Role{pP: RoleEditor},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := seed(t)
			give(t, db, member, c.grants...)
			// Another User's grants never leak in.
			give(t, db, 3, row{pR, 0, "editor"})
			access, err := Load(context.Background(), db, member, c.admin)
			if err != nil {
				t.Fatal(err)
			}
			if access.Admin != c.admin {
				t.Fatalf("Admin = %v", access.Admin)
			}
			for env, want := range c.want.roles {
				if got := access.EnvironmentRole(env); got != want {
					t.Errorf("EnvironmentRole(%d) = %v, want %v", env, got, want)
				}
			}
			for project, want := range c.want.visible {
				if got := access.ProjectVisible(project); got != want {
					t.Errorf("ProjectVisible(%d) = %v, want %v", project, got, want)
				}
			}
			for project, want := range c.want.grant {
				if got := access.ProjectGrant(project); got != want {
					t.Errorf("ProjectGrant(%d) = %v, want %v", project, got, want)
				}
			}
			for project, want := range c.want.envs {
				if got := access.VisibleEnvironments(project); !reflect.DeepEqual(got, want) {
					t.Errorf("VisibleEnvironments(%d) = %v, want %v", project, got, want)
				}
			}
			if c.admin && access.View() != nil {
				t.Errorf("View() for an admin = %+v, want nil", access.View())
			}
		})
	}
}

func TestAccessViewListsVisibleOnly(t *testing.T) {
	db := seed(t)
	give(t, db, member, row{pP, 0, "editor"}, row{pP, e1, "none"}, row{pQ, 0, "viewer"})
	access, err := Load(context.Background(), db, member, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(access.View())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"projects":[{"id":1,"role":"editor","environments":[{"id":12,"role":"editor"}]},{"id":2,"role":"viewer","environments":[]}]}`
	if string(got) != want {
		t.Fatalf("View() = %s\nwant      %s", got, want)
	}
	if all := access.AllVisibleEnvironments(); !reflect.DeepEqual(all, []int64{e2}) {
		t.Fatalf("AllVisibleEnvironments() = %v, want [12]", all)
	}
}

func TestEnvironmentOnlyGrantHasNullProjectRole(t *testing.T) {
	db := seed(t)
	give(t, db, member, row{pP, e2, "viewer"})
	access, err := Load(context.Background(), db, member, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(access.View())
	want := `{"projects":[{"id":1,"role":null,"environments":[{"id":12,"role":"viewer"}]}]}`
	if string(got) != want {
		t.Fatalf("View() = %s\nwant      %s", got, want)
	}
}
