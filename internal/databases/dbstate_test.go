package databases

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stateAdmin scripts the Postgres admin command: an inspect prints prev, the
// settings query prints settings, and every stdin is kept in order.
func (r *rig) stateAdmin(prev, settings string) *[]string {
	var got []string
	r.host.script(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		sql := string(b)
		got = append(got, sql)
		switch {
		case strings.Contains(sql, "pg_get_userbyid"):
			_, _ = io.WriteString(req.Stdout, prev)
		case strings.Contains(sql, "pg_db_role_setting"):
			_, _ = io.WriteString(req.Stdout, settings)
		}
		return 0, nil
	})
	return &got
}

func sqlFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Review Focus 2: names that need quoting survive capture and reapply.
func TestCaptureStateQuotedNames(t *testing.T) {
	for _, name := range []string{"my-app", "Shop"} {
		r := newRig(t)
		ctx := context.Background()
		d := r.create(t, "Pg", "postgres", "16")
		d, _ = r.store.Get(ctx, d.ID)
		setting := `ALTER ROLE "report""x" IN DATABASE "` + name + `" SET "role" TO 'app';`
		admin := r.stateAdmin("app\t{=Tc/app,\"\\\"report\\\"\\\"x\\\"=c/app\"}\n", settingsRow(`report"x`, "role=app"))
		restored := r.captureRestore(pgRestore, "")
		st, _, err := r.svc.captureState(ctx, d, pgDumper{}, name)
		if err != nil {
			t.Fatal(err)
		}
		if st.Name != name || !strings.HasPrefix(st.Prev, "app\t") || strings.TrimSpace(st.Settings) != setting {
			t.Fatalf("state = %+v", st)
		}
		if _, err := r.svc.restoreInto(ctx, d, pgDumper{}, st, sqlFile(t, "CREATE TABLE t();\n")); err != nil {
			t.Fatal(err)
		}
		all := strings.Join(*admin, "\n---\n")
		for _, want := range []string{`DROP DATABASE IF EXISTS "` + name + `"`, `GRANT CONNECT ON DATABASE "` + name + `" TO "report""x"`, setting} {
			if !strings.Contains(all, want) {
				t.Fatalf("%s: admin SQL lacks %q:\n%s", name, want, all)
			}
		}
		if len(*restored) != 1 || (*restored)[0] != "CREATE TABLE t();\n" {
			t.Fatalf("restored = %q", *restored)
		}
	}
}

func TestRestoreIntoAppliesSettingsLast(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Pg", "postgres", "16")
	d, _ = r.store.Get(ctx, d.ID)
	admin := r.stateAdmin("", "")
	r.captureRestore(pgRestore, "")
	r.host.reset()
	st := dbState{Name: "shop", Prev: "app", Settings: "ALTER DATABASE \"shop\" SET work_mem TO '8MB';\n"}
	if _, err := r.svc.restoreInto(ctx, d, pgDumper{}, st, sqlFile(t, "SELECT 1;\n")); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range r.host.all() {
		switch {
		case strings.Contains(c, pgAdmin):
			order = append(order, "admin")
		case strings.Contains(c, pgRestore):
			order = append(order, "restore")
		}
	}
	if strings.Join(order, ",") != "admin,restore,admin" {
		t.Fatalf("order = %v", order)
	}
	if last := (*admin)[len(*admin)-1]; last != st.Settings {
		t.Fatalf("last admin SQL = %q", last)
	}
}

// DROP DATABASE deletes a database's ALTER ROLE … IN DATABASE … SET rows, and
// a one-database dump does not carry them: a replace restore puts them back,
// or a Full user would stop acting as the owner (ADR-0044).
func TestReplaceRestoreKeepsRoleSettings(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	r.dumps("pg_dump -U postgres", "ORIGINAL;\n")
	b := r.manualBackup(t, d.ID)
	setting := `ALTER ROLE "full_u" IN DATABASE "app" SET "role" TO 'app_owner';`
	admin := r.stateAdmin("app_owner\n", settingsRow("full_u", "role=app_owner"))
	r.captureRestore(pgRestore, "")
	if _, err := r.svc.RestoreFromBackup(ctx, d.ID, b.ID, "app", "Shop"); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	if got := r.lastRestore(t, d.ID); got.Status != JobSucceeded {
		t.Fatalf("restore = %+v", got)
	}
	sqls := *admin
	drop := -1
	for i, s := range sqls {
		if strings.HasPrefix(s, `DROP DATABASE IF EXISTS "app"`) {
			drop = i
		}
	}
	if drop < 1 || !strings.Contains(strings.Join(sqls[:drop], "\n"), "pg_db_role_setting") {
		t.Fatalf("settings not read before the drop: %q", sqls)
	}
	if last := sqls[len(sqls)-1]; strings.TrimSpace(last) != setting {
		t.Fatalf("last admin SQL = %q, want the setting", last)
	}
}

// Review: the psql admin sessions (on template1) start on the catalog
// search_path too, whatever a role or database setting says.
func TestPostgresAdminCommandPinsSearchPath(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Pg", "postgres", "16")
	d, _ = r.store.Get(ctx, d.ID)
	var env []string
	r.host.script(pgAdmin, func(req ExecRequest) (int, error) {
		env = req.Env
		return 0, nil
	})
	if _, _, err := r.svc.admin(ctx, d, pgDumper{}, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "PGOPTIONS=-c search_path=pg_catalog,pg_temp") {
		t.Fatalf("env = %q", env)
	}
}
