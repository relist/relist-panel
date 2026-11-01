package panelupdate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Preflight is the Updater's dry run (spec "Preflight (no writes)"). It runs
// seven steps in order and stops at the first that fails. It writes only a
// scratch directory under the data dir and never touches the Install.

// ReadmeURL is where the README explains a manual update.
const ReadmeURL = "https://github.com/relist/relist-panel#upgrading-an-existing-install"

const (
	// envImageKey is the one .env key the Updater writes.
	envImageKey = "RELIST_PANEL_IMAGE"

	helperTimeout = 2 * time.Minute
	pullTimeout   = 15 * time.Minute
)

// Preflight step names, as the Failure of a report names them.
const (
	StepGate       = "gate"
	StepInstall    = "install"
	StepManifest   = "manifest"
	StepPull       = "pull"
	StepChange     = "change"
	StepConfig     = "config"
	StepInvariants = "invariants"
	StepRecreated  = "recreated"
)

// How the Install file on the host compares with the one this build embeds.
const (
	ChangeNotEdited      = "not-edited"
	ChangeEditedMerged   = "edited-merged"
	ChangeEditedConflict = "edited-conflict"
)

// ImagePuller pulls an image by reference through the Engine API.
type ImagePuller interface {
	Pull(ctx context.Context, ref string) error
}

// ServiceHash is the config-hash label of one Container of a Compose project.
type ServiceHash struct {
	Service string
	Hash    string
}

// ProjectInspector lists the config-hash of every Container of a project.
type ProjectInspector interface {
	ConfigHashes(ctx context.Context, project string) ([]ServiceHash, error)
}

// Docker is the Engine API the Preflight needs beyond the helper.
type Docker interface {
	ImagePuller
	ProjectInspector
	// ImageID is the ID of a pulled image, which a Container's image matches;
	// a digest reference is a registry manifest digest, not that ID.
	ImageID(ctx context.Context, ref string) (string, error)
}

// Report is the result of one Preflight.
type Report struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	FromVersion string    `json:"fromVersion"`
	ToVersion   string    `json:"toVersion"`
	NotesURL    string    `json:"notesUrl"`
	// Ready is true when nothing stops an update: no step failed, no
	// invariant fails, and the version needs no manual update.
	Ready bool `json:"ready"`
	// Failed is the step that stopped the Preflight, nil when all ran.
	Failed *Failure `json:"failed"`
	// NeedsManualUpdate is true when the manifest names an invariant kind
	// this build does not know.
	NeedsManualUpdate bool     `json:"needsManualUpdate"`
	UnknownKinds      []string `json:"unknownKinds"`
	ReadmeURL         string   `json:"readmeUrl"`

	// UpToDate is true when the newest release is not newer than the running
	// version: there is nothing to apply.
	UpToDate bool `json:"upToDate"`
	// Fingerprint hashes every input of the Preflight: Apply refuses a report
	// whose inputs have changed since the operator saw it.
	Fingerprint string `json:"fingerprint"`
	// Image is the manifest's digest reference.
	Image string `json:"image"`

	// Change is one of the Change constants; empty before the change check.
	Change string `json:"change"`
	// Diff is the change to the Install file the update would write; empty
	// when the Install file is replaced as it is.
	Diff     string    `json:"diff"`
	Conflict *Conflict `json:"conflict"`

	Invariants []InvariantResult `json:"invariants"`
	// Recreated are the Services `up` will recreate, sorted.
	Recreated []string `json:"recreated"`
	// TraefikRecreated warns that every Domain drops for a few seconds.
	TraefikRecreated bool         `json:"traefikRecreated"`
	ManualSteps      []ManualStep `json:"manualSteps"`
}

// Failure is the step that stopped a Preflight, with the reason in words the
// operator can act on.
type Failure struct {
	Step    string `json:"step"`
	Message string `json:"message"`
}

// Gate is the live state the update waits on; the page shows it next to the
// report.
type Gate struct {
	InstallAvailable bool `json:"installAvailable"`
	MigrationLocked  bool `json:"migrationLocked"`
	DeploysBusy      bool `json:"deploysBusy"`
	// UpdateRunning is true while the update lock is on: a run is in progress.
	UpdateRunning bool `json:"updateRunning"`
}

// Conflict shows both sides of an Install file edit that does not merge.
type Conflict struct {
	Ours   string `json:"ours"`   // the file on the host
	Theirs string `json:"theirs"` // the new version's file
	Merged string `json:"merged"` // the merge, conflict markers in it
	Hunks  int    `json:"hunks"`
}

// InvariantResult is one Install invariant, passing or failing.
type InvariantResult struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	OK   bool   `json:"ok"`
	// Detail says why it fails.
	Detail string `json:"detail,omitempty"`
	// Source says which file sets the value of a failing invariant:
	// "docker-compose.override.yml" or ".env", or the Install file.
	Source string `json:"source,omitempty"`
}

// conflictMessage is what a conflicted merge tells the operator.
const conflictMessage = "The Install file was edited and the edits do not merge with the new version. " +
	"Move the edits to docker-compose.override.yml, restore the Install file, and run the preflight again."

// mergeFile runs `git merge-file -p ours base theirs`. Exit 0 merged, 1 to
// 127 is the number of conflicts, and 255 an error.
func mergeFile(ours, base, theirs []byte) (merged []byte, conflicts int, err error) {
	dir, err := os.MkdirTemp("", "relist-merge-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	for name, data := range map[string][]byte{"ours": ours, "base": base, "theirs": theirs} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, 0, err
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", "merge-file", "-p", "-L", "Install file on this host", "-L", "embedded copy", "-L", "new version",
		filepath.Join(dir, "ours"), filepath.Join(dir, "base"), filepath.Join(dir, "theirs"))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var exit *exec.ExitError
	switch {
	case runErr == nil:
		return stdout.Bytes(), 0, nil
	case errors.As(runErr, &exit) && exit.ExitCode() >= 1 && exit.ExitCode() <= 127:
		return stdout.Bytes(), exit.ExitCode(), nil
	case errors.As(runErr, &exit):
		return nil, 0, fmt.Errorf("Could not merge the Install file: %s", strings.TrimSpace(stderr.String()))
	default:
		return nil, 0, fmt.Errorf("Could not run git to merge the Install file: %w", runErr)
	}
}

// unifiedDiff is what changes from a to b, empty when nothing does.
func unifiedDiff(a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}
	dir, err := os.MkdirTemp("", "relist-diff-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(dir)
	if os.WriteFile(filepath.Join(dir, "current"), a, 0o600) != nil || os.WriteFile(filepath.Join(dir, "new"), b, 0o600) != nil {
		return ""
	}
	cmd := exec.Command("git", "diff", "--no-index", "--no-color", "--", "current", "new")
	cmd.Dir = dir
	out, _ := cmd.Output() // exit 1 means "differ"
	text := string(out)
	if i := strings.Index(text, "--- "); i >= 0 {
		text = text[i:]
	}
	return text
}

// fileSet is the candidate's files in scratch, in -f order.
type fileSet struct {
	install   string
	overrides []string
	env       string
}

func (f fileSet) all() []string              { return append([]string{f.install}, f.overrides...) }
func (f fileSet) withoutOverrides() []string { return []string{f.install} }

// writeCandidate puts the candidate Install file, copies of the other config
// files and the candidate .env in the scratch directory.
func writeCandidate(dir string, in Install, files InstallFiles, candidate, env []byte) (fileSet, error) {
	set := fileSet{install: filepath.Join(dir, "docker-compose.yml"), env: filepath.Join(dir, ".env")}
	if err := os.WriteFile(set.install, candidate, 0o600); err != nil {
		return set, err
	}
	for i := 1; i < len(in.ConfigFiles); i++ {
		path := filepath.Join(dir, fmt.Sprintf("%02d-%s", i, filepath.Base(in.ConfigFiles[i])))
		if err := os.WriteFile(path, files.Config[i], 0o600); err != nil {
			return set, err
		}
		set.overrides = append(set.overrides, path)
	}
	return set, os.WriteFile(set.env, env, 0o600)
}

// composeArgs is the `docker compose` command line. The project directory is
// the real Install directory, so relative paths in the operator's files
// resolve to their real paths; the files are the scratch copies, in label
// order, and the candidate .env replaces the project one.
func composeArgs(in Install, scratch string, files []string, rest ...string) []string {
	return append([]string{"docker", "compose"}, composeSubArgs(in, scratch, files, rest...)...)
}

// composeSubArgs is composeArgs without the leading `docker compose`.
func composeSubArgs(in Install, scratch string, files []string, rest ...string) []string {
	argv := []string{"-p", in.Project, "--project-directory", in.Dir}
	for _, f := range files {
		argv = append(argv, "-f", f)
	}
	argv = append(argv, "--env-file", filepath.Join(scratch, ".env"))
	return append(argv, rest...)
}

// blame names the file a failing invariant comes from: the override when the
// invariant passes without it, else the Install file, or .env for the image.
func blame(inv Invariant, bare *composeConfig, hasOverride bool, env invariantEnv) string {
	if hasOverride && bare != nil && evaluate(inv, bare, env).OK {
		return "the Install override"
	}
	if inv.Kind == "service-image" {
		return ".env"
	}
	return "the Install file"
}

// parseHashes reads `config --hash` output: one "service hash" per line.
func parseHashes(out []byte) map[string]string {
	hashes := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			hashes[f[0]] = f[1]
		}
	}
	return hashes
}

// diffHashes lists, sorted, the Services with a Container whose hash is not
// the wanted one. A Service with no Container is not recreated, only created.
func diffHashes(want map[string]string, running []ServiceHash) []string {
	set := map[string]bool{}
	for _, c := range running {
		if w, ok := want[c.Service]; ok && w != c.Hash {
			set[c.Service] = true
		}
	}
	out := make([]string, 0, len(set))
	for svc := range set {
		out = append(out, svc)
	}
	sort.Strings(out)
	return out
}

// setEnvKey sets key in .env content: every existing assignment of it is
// replaced, and one is appended when there is none. Other lines are kept
// byte for byte.
func setEnvKey(env []byte, key, value string) []byte {
	var out []string
	found := false
	text := string(env)
	if text != "" {
		for _, line := range strings.SplitAfter(text, "\n") {
			if line == "" {
				continue
			}
			if name, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "="); ok && strings.TrimSpace(name) == key {
				if !found {
					out = append(out, key+"="+value+"\n")
				}
				found = true
				continue
			}
			out = append(out, line)
		}
	}
	if !found {
		if n := len(out); n > 0 && !strings.HasSuffix(out[n-1], "\n") {
			out[n-1] += "\n"
		}
		out = append(out, key+"="+value+"\n")
	}
	return []byte(strings.Join(out, ""))
}

// parseEnv reads KEY=VALUE lines of a .env, for expanding ${…} in invariants.
// It handles comments and single or double quotes, which is all the Install
// file's own variables use.
func parseEnv(env []byte) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(string(env), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
			value = value[1 : n-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		vars[strings.TrimSpace(name)] = value
	}
	return vars
}

// changeCheck is the change check against base, the embedded copy.
func changeCheck(r *Report, base, ours, theirs []byte) ([]byte, error) {
	if bytes.Equal(ours, base) {
		r.Change = ChangeNotEdited
		r.Diff = unifiedDiff(ours, theirs)
		return theirs, nil
	}
	merged, conflicts, err := mergeFile(ours, base, theirs)
	if err != nil {
		return nil, err
	}
	if conflicts > 0 {
		r.Change = ChangeEditedConflict
		r.Conflict = &Conflict{Ours: string(ours), Theirs: string(theirs), Merged: string(merged), Hunks: conflicts}
		return nil, nil
	}
	r.Change = ChangeEditedMerged
	r.Diff = unifiedDiff(ours, merged)
	return merged, nil
}

func newReportID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
