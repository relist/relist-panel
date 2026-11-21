package dbquery

import "testing"

func TestAdminPgConfigSilencesStatements(t *testing.T) {
	cfg, err := adminPgConfig(Target{Host: "relist-db-1", Port: 5432, User: "postgres", Password: "pw"}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RuntimeParams["log_min_error_statement"]; got != "panic" {
		t.Errorf("log_min_error_statement = %q, want panic", got)
	}
	if got := cfg.RuntimeParams["log_statement"]; got != "none" {
		t.Errorf("log_statement = %q, want none", got)
	}
	if _, ok := cfg.RuntimeParams["default_transaction_read_only"]; ok {
		t.Error("an admin connection must not be read-only")
	}
	if cfg.Database != "app" {
		t.Errorf("Database = %q, want app", cfg.Database)
	}
	if cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
		t.Error("an admin connection is plain TCP with no fallback")
	}
	def, _ := adminPgConfig(Target{Host: "h", Port: 5432, User: "postgres"}, "")
	if def.Database != "postgres" {
		t.Errorf("default Database = %q, want postgres", def.Database)
	}
}

// Security review: a database's owner can set its search_path ahead of
// pg_catalog and shadow the operators the panel's catalog queries use; the
// superuser sessions the panel opens inside it would run the owner's code.
func TestSuperuserCatalogSessionsPinSearchPath(t *testing.T) {
	tg := Target{Host: "relist-db-1", Port: 5432, User: "postgres", Password: "pw"}
	admin, err := adminPgConfig(tg, "app")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaPgConfig(tg, "app")
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]map[string]string{"admin": admin.RuntimeParams, "schema": schema.RuntimeParams} {
		if cfg["search_path"] != "pg_catalog, pg_temp" {
			t.Errorf("%s search_path = %q", name, cfg["search_path"])
		}
	}
	if schema.RuntimeParams["default_transaction_read_only"] != "on" || schema.Database != "app" {
		t.Errorf("schema config = %+v", schema.RuntimeParams)
	}
}
