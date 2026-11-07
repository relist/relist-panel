package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SystemUser is the hidden htpasswd login the panel pulls and lists tags with.
const SystemUser = "relist-panel"

// RenderConfig is registry:3's config.yml (probed 2026-11-06). readOnly turns
// on maintenance mode for garbage collection: pulls work, pushes get 405.
func RenderConfig(readOnly bool) []byte {
	return []byte(fmt.Sprintf(`# Written by the panel (ADR-0033); edits here are overwritten.
version: 0.1
log:
  level: info
storage:
  delete:
    enabled: true
  filesystem:
    rootdirectory: /var/lib/registry
  maintenance:
    readonly:
      enabled: %t
http:
  addr: :5000
  relativeurls: false
auth:
  htpasswd:
    realm: relist-registry
    path: /etc/distribution/htpasswd
`, readOnly))
}

// RenderHtpasswd lists the system login first, then each Push credential by
// name.
func RenderHtpasswd(systemHash string, creds []Credential) []byte {
	sorted := slices.Clone(creds)
	slices.SortFunc(sorted, func(a, b Credential) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	b.WriteString(SystemUser + ":" + systemHash + "\n")
	for _, c := range sorted {
		b.WriteString(c.Name + ":" + c.Hash + "\n")
	}
	return []byte(b.String())
}

// Files is <data dir>/.registry, mounted read-only into the registry as a
// directory so a renamed file is seen without a restart.
type Files struct{ Dir string }

// Write replaces config.yml and htpasswd, each by temp file and rename.
func (f Files) Write(config, htpasswd []byte) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(f.Dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(f.Dir, "config.yml"), config, 0o644); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(f.Dir, "htpasswd"), htpasswd, 0o600)
}

// ReadOnly reads the maintenance flag back from config.yml; no file is false.
func (f Files) ReadOnly() (bool, error) {
	raw, err := os.ReadFile(filepath.Join(f.Dir, "config.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cfg struct {
		Storage struct {
			Maintenance struct {
				Readonly struct {
					Enabled bool `yaml:"enabled"`
				} `yaml:"readonly"`
			} `yaml:"maintenance"`
		} `yaml:"storage"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return false, err
	}
	return cfg.Storage.Maintenance.Readonly.Enabled, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
