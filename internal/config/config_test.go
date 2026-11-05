package config

import (
	"os"
	"testing"

	"relist-panel/internal/policy"
)

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

func TestLoadInstallSettings(t *testing.T) {
	t.Setenv("RELIST_DOMAIN", "panel.example.com")
	t.Setenv("RELIST_ACME_EMAIL", "ops@example.com")
	t.Setenv("TRAEFIK_HTTP_PORT", "8000")
	t.Setenv("TRAEFIK_HTTPS_PORT", "8443")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := InstallSettings{Domain: "panel.example.com", ACMEEmail: "ops@example.com", HTTPPort: "8000", HTTPSPort: "8443"}
	if cfg.Install != want {
		t.Fatalf("Install = %+v, want %+v", cfg.Install, want)
	}
}

func TestLoadMigrationSettings(t *testing.T) {
	t.Setenv("RELIST_CONTROL_SOCKET", "/tmp/relist.sock")
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{"relist-panel", "--insecure-migration-source"}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ControlSocket != "/tmp/relist.sock" || !cfg.InsecureOldHost {
		t.Fatalf("ControlSocket = %q, InsecureOldHost = %v", cfg.ControlSocket, cfg.InsecureOldHost)
	}
}

func TestLoadControlSocketDefault(t *testing.T) {
	t.Setenv("RELIST_CONTROL_SOCKET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ControlSocket != "/run/relist-panel/control.sock" {
		t.Fatalf("ControlSocket = %q", cfg.ControlSocket)
	}
}

func TestLoadPolicy(t *testing.T) {
	t.Setenv("RELIST_POLICY_APPROVER", "owner")
	t.Setenv("RELIST_POLICY_BIND_ROOTS", "/srv, /media/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.Approver != policy.ApproverOwner || len(cfg.Policy.Roots) != 2 || cfg.Policy.Roots[1] != "/media" {
		t.Fatalf("Policy = %+v", cfg.Policy)
	}
	for name, value := range map[string]string{"RELIST_POLICY_APPROVER": "everyone", "RELIST_POLICY_BIND_ROOTS": "relative"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s was accepted", name, value)
			}
		})
	}
}
