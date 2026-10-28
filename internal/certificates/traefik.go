package certificates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"gopkg.in/yaml.v3"
)

const fileHeader = "# Written by relist-panel from the certificates table. Edits are overwritten.\n"

// certFilePattern matches only the files WriteDir owns, so the operator's own
// dynamic config in the same directory is left alone.
var certFilePattern = regexp.MustCompile(`^cert-\d+\.yml$`)

// DynamicDir is the directory Traefik's file provider watches. The install
// mounts <data dir>/.traefik at /letsencrypt, so Traefik sees it as
// /letsencrypt/dynamic.
func DynamicDir(dataDir string) string {
	return filepath.Join(dataDir, ".traefik", "dynamic")
}

func fileName(id int64) string { return fmt.Sprintf("cert-%d.yml", id) }

type dynamicConfig struct {
	TLS struct {
		Certificates []dynamicCertificate `yaml:"certificates"`
	} `yaml:"tls"`
}

// Traefik reads certFile and keyFile as a path when one exists, otherwise as
// the PEM itself, so the panel and Traefik need not agree on paths.
type dynamicCertificate struct {
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
}

// WriteDir makes dir hold exactly one file per certificate. It removes every
// cert-<id>.yml with no certificate first, so a deleted one stops being
// served even when a write fails, and it attempts every write before
// returning their joined errors.
func WriteDir(dir string, certs []Certificate) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, c := range certs {
		keep[fileName(c.ID)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !certFilePattern.MatchString(e.Name()) || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, c := range certs {
		if err := writeFile(dir, fileName(c.ID), c); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", fileName(c.ID), err))
		}
	}
	return errors.Join(errs...)
}

// writeFile writes through a temporary file and a rename, so Traefik's
// watcher never reads half a file.
func writeFile(dir, name string, c Certificate) error {
	var doc dynamicConfig
	doc.TLS.Certificates = []dynamicCertificate{{CertFile: c.CertificatePEM, KeyFile: c.PrivateKeyPEM}}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cert-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(fileHeader + string(body)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// reconcileListed runs between Reconcile's List and its writes; tests pause
// there.
var reconcileListed = func() {}

// reconcileMu makes Reconciles run one at a time, so one that listed before
// another write landed cannot then remove or overwrite that write's file.
var reconcileMu sync.Mutex

// Reconcile rewrites Traefik's certificate files from the database.
func Reconcile(ctx context.Context, store *Store, dataDir string) error {
	reconcileMu.Lock()
	defer reconcileMu.Unlock()
	certs, err := store.List(ctx)
	if err != nil {
		return err
	}
	reconcileListed()
	return WriteDir(DynamicDir(dataDir), certs)
}
