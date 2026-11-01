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

	Gate Gate `json:"gate"`
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

// Gate is the live state the update waits on.
type Gate struct {
	InstallAvailable bool `json:"installAvailable"`
	MigrationLocked  bool `json:"migrationLocked"`
	DeploysBusy      bool `json:"deploysBusy"`
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

// LatestReport is the newest report, nil before the first Preflight. Apply
// compares its id with the one the operator confirmed.
func (s *Service) LatestReport() *Report {
	s.pfMu.Lock()
	defer s.pfMu.Unlock()
	return s.latest
}

// Preflight runs the seven steps against the newest verified manifest. It
// returns nil when there is no manifest to prepare for.
func (s *Service) Preflight(ctx context.Context) (*Report, error) {
	m, err := s.Available(ctx)
	if err != nil || m == nil {
		return nil, err
	}
	// One at a time: the scratch directory keeps only the latest report.
	s.pfRun.Lock()
	defer s.pfRun.Unlock()
	id, err := newReportID()
	if err != nil {
		return nil, err
	}
	r := &Report{
		ID: id, CreatedAt: s.d.Now().UTC(), FromVersion: s.d.Version, ToVersion: m.Version,
		NotesURL: m.NotesURL, ReadmeURL: ReadmeURL,
		Invariants: []InvariantResult{}, Recreated: []string{}, UnknownKinds: []string{},
		ManualSteps: m.ManualSteps,
	}
	if r.ManualSteps == nil {
		r.ManualSteps = []ManualStep{}
	}
	s.runSteps(ctx, m, r)
	s.pfMu.Lock()
	s.latest = r
	s.pfMu.Unlock()
	return r, nil
}

func (s *Service) runSteps(ctx context.Context, m *Manifest, r *Report) {
	fail := func(step, message string) { r.Failed = &Failure{Step: step, Message: message} }

	// 1. Gate.
	in, err := s.gate(ctx, r)
	if err != nil {
		fail(StepGate, err.Error())
		return
	}
	if unknown := UnknownKinds(m.Invariants); len(unknown) > 0 {
		r.NeedsManualUpdate, r.UnknownKinds = true, unknown
		return
	}
	// 2. Pull.
	if err := s.pull(ctx, m); err != nil {
		fail(StepPull, err.Error())
		return
	}
	// 3. Change check.
	files, err := s.ReadInstallFiles(ctx, in)
	if err != nil {
		fail(StepChange, "Could not read the Install's files: "+err.Error())
		return
	}
	candidate, err := s.changeCheck(r, files.Config[0], []byte(m.InstallFile))
	if err != nil {
		fail(StepChange, err.Error())
		return
	}
	if r.Change == ChangeEditedConflict {
		fail(StepChange, conflictMessage)
		return
	}
	// 4. Config.
	scratch, err := s.newScratch(r.ID)
	if err != nil {
		fail(StepConfig, "Could not prepare the scratch directory: "+err.Error())
		return
	}
	env := setEnvKey(files.Env, envImageKey, m.Image)
	set, err := writeCandidate(scratch, in, files, candidate, env)
	if err != nil {
		fail(StepConfig, "Could not write the candidate files: "+err.Error())
		return
	}
	merged, err := s.composeConfigJSON(ctx, in, scratch, set.all())
	if err != nil {
		fail(StepConfig, err.Error())
		return
	}
	cfg, err := parseComposeConfig(merged)
	if err != nil {
		fail(StepConfig, err.Error())
		return
	}
	// 5. Invariants.
	vars := parseEnv(env)
	r.Invariants = s.checkInvariants(ctx, in, scratch, set, m, cfg, vars)
	// 6. Recreated Services.
	recreated, err := s.recreated(ctx, in, scratch, set.all())
	if err != nil {
		fail(StepRecreated, err.Error())
		return
	}
	r.Recreated = recreated
	for _, svc := range recreated {
		if svc == "traefik" {
			r.TraefikRecreated = true
		}
	}
	// 7. The report is what is left.
	r.Ready = true
	for _, inv := range r.Invariants {
		r.Ready = r.Ready && inv.OK
	}
}

func newReportID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// gate is step 1: no Migration lock is held and the Install is found. The
// Install is found fresh, as the spec asks, before every Preflight.
func (s *Service) gate(ctx context.Context, r *Report) (Install, error) {
	if s.d.MigrationLocked != nil {
		locked, err := s.d.MigrationLocked(ctx)
		if err != nil {
			return Install{}, fmt.Errorf("Could not read the Migration state: %w", err)
		}
		r.Gate.MigrationLocked = locked
	}
	if s.d.DeploysBusy != nil {
		r.Gate.DeploysBusy = s.d.DeploysBusy()
	}
	if r.Gate.MigrationLocked {
		return Install{}, errors.New("A server Migration holds the lock; Panel update waits until it is over.")
	}
	in, err := s.FindInstall(ctx)
	if err != nil {
		var unavailable *Unavailable
		if errors.As(err, &unavailable) {
			return Install{}, fmt.Errorf("Panel update is unavailable: %s.", unavailable.Reason)
		}
		return Install{}, err
	}
	r.Gate.InstallAvailable = true
	if len(s.d.InstallFile) == 0 {
		return Install{}, errors.New("This build embeds no Install file to compare with.")
	}
	return in, nil
}

// pull is step 2: the manifest's image, by digest. Docker's words are the
// message.
func (s *Service) pull(ctx context.Context, m *Manifest) error {
	if !strings.Contains(m.Image, "@sha256:") {
		return fmt.Errorf("The manifest's image %q is not a digest reference.", m.Image)
	}
	if s.d.Docker == nil {
		return errors.New("This panel cannot reach Docker to pull the image.")
	}
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	if err := s.d.Docker.Pull(ctx, m.Image); err != nil {
		return fmt.Errorf("Could not pull %s: %w", m.Image, err)
	}
	return nil
}

// changeCheck is step 3. It compares the Install file with the embedded copy
// and returns the file the update would write: theirs as it is when the file
// is not edited, the three-way merge when it is. A conflict sets the report's
// Conflict and returns no candidate.
func (s *Service) changeCheck(r *Report, ours, theirs []byte) ([]byte, error) {
	if bytes.Equal(ours, s.d.InstallFile) {
		r.Change = ChangeNotEdited
		r.Diff = unifiedDiff(ours, theirs)
		return theirs, nil
	}
	merged, conflicts, err := mergeFile(ours, s.d.InstallFile, theirs)
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

// scratchRoot holds the Preflight scratch directories.
func (s *Service) scratchRoot() string {
	return filepath.Join(s.d.DataDir, ".panel-update", "preflight")
}

// newScratch makes <data dir>/.panel-update/preflight/<id>/ and removes the
// older ones, so only the latest is kept.
func (s *Service) newScratch(id string) (string, error) {
	if s.d.DataDir == "" {
		return "", errors.New("no data dir")
	}
	root := s.scratchRoot()
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
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
	if err := os.WriteFile(set.install, candidate, 0o644); err != nil {
		return set, err
	}
	for i := 1; i < len(in.ConfigFiles); i++ {
		path := filepath.Join(dir, fmt.Sprintf("%02d-%s", i, filepath.Base(in.ConfigFiles[i])))
		if err := os.WriteFile(path, files.Config[i], 0o644); err != nil {
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
	argv := []string{"docker", "compose", "-p", in.Project, "--project-directory", in.Dir}
	for _, f := range files {
		argv = append(argv, "-f", f)
	}
	argv = append(argv, "--env-file", filepath.Join(scratch, ".env"))
	return append(argv, rest...)
}

// runCompose runs `docker compose` in the read-only helper. A failure is
// Compose's own words.
func (s *Service) runCompose(ctx context.Context, in Install, scratch string, files []string, rest ...string) ([]byte, error) {
	if s.d.Helper == nil {
		return nil, errors.New("This panel cannot start a helper Container.")
	}
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	var stdout bytes.Buffer
	err := s.d.Helper.Run(ctx, HelperRun{
		Image: in.Image, Dir: in.Dir, Scratch: scratch, Argv: composeArgs(in, scratch, files, rest...),
	}, &stdout)
	var exit *HelperExitError
	if errors.As(err, &exit) {
		message := strings.TrimSpace(exit.Stderr)
		if message == "" {
			message = fmt.Sprintf("docker compose exited %d", exit.Code)
		}
		return nil, fmt.Errorf("Docker Compose rejected the configuration: %s", message)
	}
	if err != nil {
		return nil, fmt.Errorf("Could not run Docker Compose: %w", err)
	}
	return stdout.Bytes(), nil
}

// composeConfigJSON is step 4's `config --format json`.
func (s *Service) composeConfigJSON(ctx context.Context, in Install, scratch string, files []string) ([]byte, error) {
	return s.runCompose(ctx, in, scratch, files, "config", "--format", "json")
}

// checkInvariants is step 5: every invariant, passing and failing. A failing
// one says which file sets its value.
func (s *Service) checkInvariants(ctx context.Context, in Install, scratch string, set fileSet, m *Manifest, cfg *composeConfig, vars map[string]string) []InvariantResult {
	env := invariantEnv{Image: m.Image, Vars: vars}
	var bare *composeConfig // the config without the Install override, read once and only if needed
	bareRead := false
	results := make([]InvariantResult, 0, len(m.Invariants))
	for _, inv := range m.Invariants {
		o := evaluate(inv, cfg, env)
		res := InvariantResult{Kind: inv.Kind, Text: o.Text, OK: o.OK, Detail: o.Detail}
		if !o.OK {
			if !bareRead && len(set.overrides) > 0 {
				bare, bareRead = s.configWithoutOverride(ctx, in, scratch, set), true
			}
			res.Source = blame(inv, bare, len(set.overrides) > 0, env)
		}
		results = append(results, res)
	}
	return results
}

// configWithoutOverride re-runs `config` with the Install file alone. Nil when
// that fails, in which case nothing is blamed on the override.
func (s *Service) configWithoutOverride(ctx context.Context, in Install, scratch string, set fileSet) *composeConfig {
	out, err := s.composeConfigJSON(ctx, in, scratch, set.withoutOverrides())
	if err != nil {
		return nil
	}
	cfg, err := parseComposeConfig(out)
	if err != nil {
		return nil
	}
	return cfg
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

// recreated is step 6: the Services whose config-hash differs from the one
// their running Container carries.
func (s *Service) recreated(ctx context.Context, in Install, scratch string, files []string) ([]string, error) {
	if s.d.Docker == nil {
		return nil, errors.New("This panel cannot reach Docker to read the running Containers.")
	}
	out, err := s.runCompose(ctx, in, scratch, files, "config", "--hash", "*")
	if err != nil {
		return nil, err
	}
	want := parseHashes(out)
	running, err := s.d.Docker.ConfigHashes(ctx, in.Project)
	if err != nil {
		return nil, fmt.Errorf("Could not read the running Containers: %w", err)
	}
	return diffHashes(want, running), nil
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
