package domains

import (
	"context"
	"path/filepath"
	"testing"

	"relist-panel/internal/database"
)

func TestRuleAndEntrypointMatchGeneratedLabels(t *testing.T) {
	cases := []Domain{
		{ID: 1, Host: "a.example.com", Port: 80},
		{ID: 2, Host: "b.example.com", Path: "/api", Port: 8080, HTTPS: true, CertProvider: "letsencrypt"},
		{ID: 3, Host: "c.example.com", Path: "/", Port: 3000, Entrypoint: "internal"},
		{ID: 4, Host: "d.example.com", Port: 3000, HTTPS: true, CertProvider: "custom", CertResolver: "dns", Entrypoint: "websecure2"},
	}
	for _, d := range cases {
		labels := readLabels(nil)
		addDomain(labels, 9, d, false)
		router := routerPrefix + RouterName(9, d.ID)
		if rule, _ := labels.get(router + ".rule"); Rule(d) != rule {
			t.Errorf("domain %d: Rule() = %q, label %q", d.ID, Rule(d), rule)
		}
		if ep, _ := labels.get(router + ".entrypoints"); EntrypointFor(d) != ep {
			t.Errorf("domain %d: EntrypointFor() = %q, label %q", d.ID, EntrypointFor(d), ep)
		}
	}
}

func TestListAllOrdersByID(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`INSERT INTO projects (name, created_at, updated_at) VALUES ('p', 1, 1)`,
		`INSERT INTO environments (project_id, name, created_at) VALUES (1, 'e', 1)`,
		`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'a', '', 1, 1)`,
		`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'b', '', 1, 1)`,
		`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at) VALUES (2, 'two.example.com', 'web', 80, 0, 0)`,
		`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at) VALUES (1, 'one.example.com', 'web', 80, 0, 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	got, err := NewStore(db).ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Host != "two.example.com" || got[0].ComposeID != 2 || got[1].Host != "one.example.com" {
		t.Fatalf("ListAll() = %+v", got)
	}
}
