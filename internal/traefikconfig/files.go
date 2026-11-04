package traefikconfig

import (
	"os"
	"path/filepath"
)

// Paths are the files of <data dir>/.traefik/static, which Traefik sees as
// /etc/traefik.
type Paths struct{ Dir string }

func PathsFor(dataDir string) Paths {
	return Paths{Dir: filepath.Join(dataDir, ".traefik", "static")}
}

// Effective is the generated file Traefik reads.
func (p Paths) Effective() string { return filepath.Join(p.Dir, "traefik.yml") }

// Override is the operator's Traefik override.
func (p Paths) Override() string { return filepath.Join(p.Dir, "traefik.override.yml") }

// ApplyFile holds the last Traefik apply.
func (p Paths) ApplyFile() string { return filepath.Join(p.Dir, "apply.json") }

func (p Paths) HistoryDir() string { return filepath.Join(p.Dir, "history") }

// DryRunDir holds one dry-run's candidate traefik.yml.
func (p Paths) DryRunDir(id string) string { return filepath.Join(p.Dir, ".dryrun-"+id) }

// WriteAtomic writes data to path.tmp, syncs it and renames it over path, so a
// reader sees the old file or the new one, never half of one.
func WriteAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
