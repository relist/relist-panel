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
