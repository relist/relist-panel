package traefikconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// historyName is <UTC time>-<run id>; nothing else under history/ is read.
var historyName = regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9]{8}$`)

var ErrNoHistory = errors.New("no such history entry")

// HistoryEntry is one backup: the files from before a run, and how the run
// that replaced them ended.
type HistoryEntry struct {
	Name   string    `json:"name"`
	Kind   Kind      `json:"kind"`
	Result string    `json:"result"`
	At     time.Time `json:"at"`
}

const historyMeta = "entry.json"

// backup copies the current override and traefik.yml into a new history
// entry and prunes the oldest beyond Keep.
func (r *Runner) backup(id string, kind Kind) (string, error) {
	at := r.now().UTC()
	name := at.Format("20060102T150405Z") + "-" + id
	dir := filepath.Join(r.Paths.HistoryDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, src := range []string{r.Paths.Override(), r.Paths.Effective()} {
		if err := copyFile(src, filepath.Join(dir, filepath.Base(src))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := r.writeMeta(name, HistoryEntry{Name: name, Kind: kind, At: at}); err != nil {
		return "", err
	}
	r.prune()
	return name, nil
}

// restoreFiles puts a history entry's files back.
func (r *Runner) restoreFiles(name string) error {
	dir := filepath.Join(r.Paths.HistoryDir(), name)
	for _, dst := range []string{r.Paths.Override(), r.Paths.Effective()} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.Base(dst)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := WriteAtomic(dst, b); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) writeMeta(name string, e HistoryEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return WriteAtomic(filepath.Join(r.Paths.HistoryDir(), name, historyMeta), b)
}

func (r *Runner) recordResult(name, result string) {
	e, err := r.readMeta(name)
	if err != nil {
		return
	}
	e.Result = result
	_ = r.writeMeta(name, e)
}

func (r *Runner) readMeta(name string) (HistoryEntry, error) {
	b, err := os.ReadFile(filepath.Join(r.Paths.HistoryDir(), name, historyMeta))
	if err != nil {
		return HistoryEntry{}, err
	}
	var e HistoryEntry
	err = json.Unmarshal(b, &e)
	e.Name = name
	return e, err
}

func (r *Runner) names() []string {
	entries, _ := os.ReadDir(r.Paths.HistoryDir())
	var names []string
	for _, e := range entries {
		if e.IsDir() && historyName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names
}

func (r *Runner) prune() {
	keep := r.Keep
	if keep <= 0 {
		keep = 10
	}
	names := r.names()
	for i := keep; i < len(names); i++ {
		_ = os.RemoveAll(filepath.Join(r.Paths.HistoryDir(), names[i]))
	}
}

// History is the backups, newest first.
func (r *Runner) History() ([]HistoryEntry, error) {
	out := []HistoryEntry{}
	for _, name := range r.names() {
		e, err := r.readMeta(name)
		if err != nil {
			e = HistoryEntry{Name: name}
		}
		out = append(out, e)
	}
	return out, nil
}

// Restore applies a history entry's Traefik override.
func (r *Runner) Restore(ctx context.Context, name string) (string, error) {
	if !historyName.MatchString(name) {
		return "", ErrNoHistory
	}
	override, err := os.ReadFile(filepath.Join(r.Paths.HistoryDir(), name, filepath.Base(r.Paths.Override())))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoHistory
	}
	if err != nil {
		return "", err
	}
	effective, err := generateOrProblems(override)
	if err != nil {
		return "", err
	}
	return r.begin(ctx, KindRestore, override, effective)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	b, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return WriteAtomic(dst, b)
}
