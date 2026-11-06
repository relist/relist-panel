package registry

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderConfigReadOnly(t *testing.T) {
	for _, ro := range []bool{false, true} {
		var cfg struct {
			Version string `yaml:"version"`
			Storage struct {
				Delete      struct{ Enabled bool } `yaml:"delete"`
				Filesystem  struct{ Rootdirectory string }
				Maintenance struct {
					Readonly struct{ Enabled bool } `yaml:"readonly"`
				} `yaml:"maintenance"`
			} `yaml:"storage"`
			HTTP struct{ Addr string } `yaml:"http"`
			Auth struct {
				Htpasswd struct{ Realm, Path string }
			} `yaml:"auth"`
		}
		if err := yaml.Unmarshal(RenderConfig(ro), &cfg); err != nil {
			t.Fatalf("readOnly=%v: %v\n%s", ro, err, RenderConfig(ro))
		}
		if cfg.Version != "0.1" || !cfg.Storage.Delete.Enabled || cfg.Storage.Filesystem.Rootdirectory != "/var/lib/registry" ||
			cfg.Storage.Maintenance.Readonly.Enabled != ro || cfg.HTTP.Addr != ":5000" ||
			cfg.Auth.Htpasswd.Realm != "relist-registry" || cfg.Auth.Htpasswd.Path != "/etc/distribution/htpasswd" {
			t.Fatalf("readOnly=%v: parsed %+v", ro, cfg)
		}
	}
}

func TestRenderHtpasswdOrder(t *testing.T) {
	got := string(RenderHtpasswd("$2a$sys", []Credential{{Name: "github-ci", Hash: "$2a$gh"}, {Name: "alpha", Hash: "$2a$al"}}))
	want := "relist-panel:$2a$sys\nalpha:$2a$al\ngithub-ci:$2a$gh\n"
	if got != want {
		t.Fatalf("htpasswd = %q, want %q", got, want)
	}
}

func TestWriteIsAtomic(t *testing.T) {
	f := Files{Dir: filepath.Join(t.TempDir(), ".registry")}
	if err := f.Write(RenderConfig(true), []byte("relist-panel:x\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(RenderConfig(false), []byte("relist-panel:y\n")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(f.Dir)
	if len(entries) != 2 {
		t.Fatalf("dir holds %v, want config.yml and htpasswd only", entries)
	}
	if info, _ := os.Stat(f.Dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", info.Mode())
	}
	if info, _ := os.Stat(filepath.Join(f.Dir, "htpasswd")); info.Mode().Perm() != 0o600 {
		t.Fatalf("htpasswd mode = %v", info.Mode())
	}
	if raw, _ := os.ReadFile(filepath.Join(f.Dir, "htpasswd")); string(raw) != "relist-panel:y\n" {
		t.Fatalf("htpasswd = %q", raw)
	}
	ro, err := f.ReadOnly()
	if err != nil || ro {
		t.Fatalf("ReadOnly = %v, %v; want false", ro, err)
	}
}

func TestReadOnlyMissingFileIsFalse(t *testing.T) {
	ro, err := Files{Dir: t.TempDir()}.ReadOnly()
	if err != nil || ro {
		t.Fatalf("ReadOnly = %v, %v", ro, err)
	}
}
