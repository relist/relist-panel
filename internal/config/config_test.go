package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("RELIST_PORT", "")
	t.Setenv("RELIST_DATA_DIR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != defaultPort {
		t.Fatalf("Port = %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.DataDir != defaultDataDir {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, defaultDataDir)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("RELIST_PORT", "9090")
	t.Setenv("RELIST_DATA_DIR", "/tmp/relist-panel-test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != 9090 || cfg.DataDir != "/tmp/relist-panel-test" {
		t.Fatalf("Load() = %+v, want port 9090 and custom data dir", cfg)
	}
	if cfg.Address() != ":9090" {
		t.Fatalf("Address() = %q, want %q", cfg.Address(), ":9090")
	}
	if cfg.DatabasePath() != "/tmp/relist-panel-test/panel.db" {
		t.Fatalf("DatabasePath() = %q", cfg.DatabasePath())
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	for _, value := range []string{"not-a-port", "0", "65536"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("RELIST_PORT", value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want an invalid-port error")
			}
		})
	}
}
