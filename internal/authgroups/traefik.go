package authgroups

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/certificates"
)

const fileHeader = "# Written by relist-panel from the auth_groups table. Edits are overwritten.\n"

// authFilePattern matches only the files WriteDir owns, so Certificates'
// files and the operator's own dynamic config are left alone.
var authFilePattern = regexp.MustCompile(`^auth-\d+\.yml$`)

// MiddlewareName is the name of the group's basicAuth middleware. A Domain
// references it as MiddlewareName(id) + "@file".
func MiddlewareName(groupID int64) string { return fmt.Sprintf("relist-auth-%d", groupID) }

func fileName(id int64) string { return fmt.Sprintf("auth-%d.yml", id) }

// GroupUsers is a group with its users, as WriteDir takes it.
type GroupUsers struct {
	Group
	Users []User
}

type basicAuth struct {
	Realm        string   `yaml:"realm"`
	RemoveHeader bool     `yaml:"removeHeader"`
	Users        []string `yaml:"users"`
}

type middleware struct {
	BasicAuth basicAuth `yaml:"basicAuth"`
}

type dynamicConfig struct {
	HTTP struct {
		Middlewares map[string]middleware `yaml:"middlewares"`
	} `yaml:"http"`
}

// lockedEntry is the single user of a group with no users: random, never
// stored, so the Domain refuses everyone instead of being open or broken.
func lockedEntry() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	username, password := hex.EncodeToString(b[:32]), hex.EncodeToString(b[32:])
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	return username + ":" + hash, nil
}

// WriteDir makes dir hold exactly one auth-<id>.yml per group. It removes
// stale ones first and attempts every write before returning their joined
// errors.
func WriteDir(dir string, groups []GroupUsers) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, g := range groups {
		keep[fileName(g.ID)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !authFilePattern.MatchString(e.Name()) || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, g := range groups {
		if err := writeFile(dir, g); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", fileName(g.ID), err))
		}
	}
	return errors.Join(errs...)
}

// writeFile writes through a temporary file and a rename, so Traefik's
// watcher never reads half a file. The body is marshalled, never formatted,
// so names and hashes with YAML-special characters survive.
func writeFile(dir string, g GroupUsers) error {
	entries := make([]string, 0, len(g.Users))
	for _, u := range g.Users {
		entries = append(entries, u.Username+":"+u.Hash)
	}
	if len(entries) == 0 {
		e, err := lockedEntry()
		if err != nil {
			return err
		}
		entries = append(entries, e)
	}
	var doc dynamicConfig
	doc.HTTP.Middlewares = map[string]middleware{
		MiddlewareName(g.ID): {BasicAuth: basicAuth{Realm: g.Name, RemoveHeader: true, Users: entries}},
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, fileName(g.ID)))
}

// reconcileMu makes Reconciles run one at a time, so one that read before
// another write landed cannot then overwrite that write's file.
var reconcileMu sync.Mutex

// Reconcile rewrites Traefik's Basic auth files from the database.
func Reconcile(ctx context.Context, store *Store, dataDir string) error {
	reconcileMu.Lock()
	defer reconcileMu.Unlock()
	groups, err := store.List(ctx)
	if err != nil {
		return err
	}
	all := make([]GroupUsers, 0, len(groups))
	for _, g := range groups {
		users, err := store.Users(ctx, g.ID)
		if err != nil {
			return err
		}
		all = append(all, GroupUsers{Group: g, Users: users})
	}
	return WriteDir(certificates.DynamicDir(dataDir), all)
}
