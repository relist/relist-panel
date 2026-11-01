package panelupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Checker is the Preflight as a standalone check: it fetches and verifies the
// Update manifest itself, so it needs neither the panel nor its database. The
// update command runs it in a Container of the current panel image. It writes
// only a scratch directory under the data dir and never touches the Install.
type Checker struct {
	DataDir string
	// Version is the running build's version.
	Version string
	// InstallFile is the Install file this build embeds: the base of the
	// change check and the merge.
	InstallFile []byte
	// Key verifies manifests. Nil means the committed release key.
	Key         ed25519.PublicKey
	ManifestURL string
	// Client fetches the manifest. Nil means a client with checkTimeout.
	Client  *http.Client
	Docker  Docker
	Compose ComposeRunner
	Now     func() time.Time
}

// Candidate is what a Preflight would write, kept in memory so Apply never
// reads the scratch directory a later Preflight removes.
type Candidate struct {
	In       Install
	Manifest *Manifest
	Files    InstallFiles
	// Install and Env are the new Install file and .env.
	Install, Env []byte
	// ImageID is the ID of the pulled image, which a Container running it
	// reports.
	ImageID string
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Check runs spec Preflight steps 1 to 8 in order against the Install. The
// error is only for "could not run at all"; a failed step is Report.Failed.
func (c *Checker) Check(ctx context.Context, in Install) (*Report, *Candidate, error) {
	id, err := newReportID()
	if err != nil {
		return nil, nil, err
	}
	r := &Report{
		ID: id, CreatedAt: c.now().UTC(), FromVersion: c.Version, ReadmeURL: ReadmeURL,
		Invariants: []InvariantResult{}, Recreated: []string{}, UnknownKinds: []string{},
		ManualSteps: []ManualStep{},
	}
	cand := &Candidate{In: in}
	c.runSteps(ctx, in, r, cand)
	r.Fingerprint = fingerprintOf(r, cand)
	return r, cand, nil
}

func (c *Checker) runSteps(ctx context.Context, in Install, r *Report, cand *Candidate) {
	fail := func(step, message string) { r.Failed = &Failure{Step: step, Message: message} }

	// 1. Manifest.
	m, err := c.manifest(ctx)
	if err != nil {
		fail(StepManifest, err.Error())
		return
	}
	cand.Manifest = m
	r.ToVersion, r.NotesURL, r.Image = m.Version, m.NotesURL, m.Image
	if m.ManualSteps != nil {
		r.ManualSteps = m.ManualSteps
	}
	running, ok := parseVersion(c.Version)
	if !ok {
		fail(StepManifest, fmt.Sprintf("This build (%q) has no release version, so it is never updated.", c.Version))
		return
	}
	if v, _ := parseVersion(m.Version); !v.greater(running) {
		r.UpToDate = true
		return
	}
	// 2. Kinds.
	if unknown := UnknownKinds(m.Invariants); len(unknown) > 0 {
		r.NeedsManualUpdate, r.UnknownKinds = true, unknown
		return
	}
	if len(c.InstallFile) == 0 {
		fail(StepChange, "This build embeds no Install file to compare with.")
		return
	}
	// 3. Pull.
	if err := c.pull(ctx, m); err != nil {
		fail(StepPull, err.Error())
		return
	}
	cand.ImageID, err = c.Docker.ImageID(ctx, m.Image)
	if err != nil {
		fail(StepPull, fmt.Sprintf("Could not read the pulled image %s: %v", m.Image, err))
		return
	}
	// 4. Change check.
	files, err := ReadLocalFiles(in)
	if err != nil {
		fail(StepChange, "Could not read the Install's files: "+err.Error())
		return
	}
	merged, err := changeCheck(r, c.InstallFile, files.Config[0], []byte(m.InstallFile))
	if err != nil {
		fail(StepChange, err.Error())
		return
	}
	if r.Change == ChangeEditedConflict {
		fail(StepChange, conflictMessage)
		return
	}
	// 5. Config.
	scratch, err := newScratchDir(c.DataDir, r.ID)
	if err != nil {
		fail(StepConfig, "Could not prepare the scratch directory: "+err.Error())
		return
	}
	env := setEnvKey(files.Env, envImageKey, m.Image)
	cand.Files, cand.Install, cand.Env = files, merged, env
	set, err := writeCandidate(scratch, in, files, merged, env)
	if err != nil {
		fail(StepConfig, "Could not write the candidate files: "+err.Error())
		return
	}
	out, err := c.compose(ctx, in, scratch, set.all(), "config", "--format", "json")
	if err != nil {
		fail(StepConfig, err.Error())
		return
	}
	cfg, err := parseComposeConfig(out)
	if err != nil {
		fail(StepConfig, err.Error())
		return
	}
	// 6. Invariants.
	r.Invariants = c.checkInvariants(ctx, in, scratch, set, m, cfg, parseEnv(env))
	// 7. Recreated Services.
	recreated, err := c.recreated(ctx, in, scratch, set.all())
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
	// 8. The report is what is left.
	r.Ready = true
	for _, inv := range r.Invariants {
		r.Ready = r.Ready && inv.OK
	}
}

// manifest fetches the latest release's manifest and signature and verifies
// them. A manifest that fails verification is an error, never shown.
func (c *Checker) manifest(ctx context.Context) (*Manifest, error) {
	key := c.Key
	if key == nil && releasePublicKey != "" {
		k, err := DecodeKey(releasePublicKey)
		if err != nil {
			return nil, fmt.Errorf("release public key: %w", err)
		}
		key = k
	}
	if err := checkKey(key); err != nil {
		return nil, ErrNoKey
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: checkTimeout}
	}
	url := c.ManifestURL
	if url == "" {
		url = DefaultManifestURL
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	data, err := httpGet(ctx, client, url)
	if err != nil {
		return nil, err
	}
	sig, err := httpGet(ctx, client, url+".sig")
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(data, sig, key)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (c *Checker) pull(ctx context.Context, m *Manifest) error {
	if !strings.Contains(m.Image, "@sha256:") {
		return fmt.Errorf("The manifest's image %q is not a digest reference.", m.Image)
	}
	if c.Docker == nil {
		return errors.New("This command cannot reach Docker to pull the image.")
	}
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	if err := c.Docker.Pull(ctx, m.Image); err != nil {
		return fmt.Errorf("Could not pull %s: %w", m.Image, err)
	}
	return nil
}

// compose runs `docker compose` against the candidate files. A failure is
// Compose's own words.
func (c *Checker) compose(ctx context.Context, in Install, scratch string, files []string, rest ...string) ([]byte, error) {
	if c.Compose == nil {
		return nil, errors.New("This command cannot run Docker Compose.")
	}
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	out, err := c.Compose.Run(ctx, composeSubArgs(in, scratch, files, rest...))
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
	return out, nil
}

func (c *Checker) checkInvariants(ctx context.Context, in Install, scratch string, set fileSet, m *Manifest, cfg *composeConfig, vars map[string]string) []InvariantResult {
	env := invariantEnv{Image: m.Image, Vars: vars}
	var bare *composeConfig // the config without the Install override, read once and only if needed
	bareRead := false
	results := make([]InvariantResult, 0, len(m.Invariants))
	for _, inv := range m.Invariants {
		o := evaluate(inv, cfg, env)
		res := InvariantResult{Kind: inv.Kind, Text: o.Text, OK: o.OK, Detail: o.Detail}
		if !o.OK {
			if !bareRead && len(set.overrides) > 0 {
				bare, bareRead = c.configWithoutOverride(ctx, in, scratch, set), true
			}
			res.Source = blame(inv, bare, len(set.overrides) > 0, env)
		}
		results = append(results, res)
	}
	return results
}

func (c *Checker) configWithoutOverride(ctx context.Context, in Install, scratch string, set fileSet) *composeConfig {
	out, err := c.compose(ctx, in, scratch, set.withoutOverrides(), "config", "--format", "json")
	if err != nil {
		return nil
	}
	cfg, err := parseComposeConfig(out)
	if err != nil {
		return nil
	}
	return cfg
}

func (c *Checker) recreated(ctx context.Context, in Install, scratch string, files []string) ([]string, error) {
	if c.Docker == nil {
		return nil, errors.New("This command cannot reach Docker to read the running Containers.")
	}
	out, err := c.compose(ctx, in, scratch, files, "config", "--hash", "*")
	if err != nil {
		return nil, err
	}
	running, err := c.Docker.ConfigHashes(ctx, in.Project)
	if err != nil {
		return nil, fmt.Errorf("Could not read the running Containers: %w", err)
	}
	return diffHashes(parseHashes(out), running), nil
}

// fingerprintOf hashes what makes two reports the same preflight: it leaves
// out the id and the time, and adds the manifest bytes and the pulled image's
// ID to what the Service's version covered.
func fingerprintOf(r *Report, c *Candidate) string {
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(struct {
		From, To, Image, Change, Diff string
		Failed                        *Failure
		Ready, Manual, UpToDate       bool
		Invariants                    []InvariantResult
		Recreated                     []string
		Steps                         []ManualStep
	}{r.FromVersion, r.ToVersion, r.Image, r.Change, r.Diff, r.Failed, r.Ready, r.NeedsManualUpdate, r.UpToDate, r.Invariants, r.Recreated, r.ManualSteps})
	if c.Manifest != nil {
		h.Write(c.Manifest.Raw)
	}
	h.Write([]byte{0})
	h.Write([]byte(c.ImageID))
	h.Write([]byte{0})
	h.Write(c.Install)
	h.Write([]byte{0})
	h.Write(c.Env)
	for _, f := range c.Files.Config {
		h.Write([]byte{0})
		h.Write(f)
	}
	h.Write([]byte{0})
	h.Write(c.Files.Env)
	return hex.EncodeToString(h.Sum(nil))
}

// ExitCode is the update command's exit code for a preflight: 3 up to date,
// 0 ready, 1 not ready.
func (r *Report) ExitCode() int {
	switch {
	case r.UpToDate:
		return 3
	case r.Ready:
		return 0
	}
	return 1
}

const reportFile = "report.json"

var reportIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidReportID reports whether id has the form newReportID makes, so it is
// safe to join into a path.
func ValidReportID(id string) bool { return reportIDPattern.MatchString(id) }

func preflightRoot(dataDir string) string {
	return filepath.Join(dataDir, runsDirName, "preflight")
}

// newScratchDir makes <data dir>/.panel-update/preflight/<id>/ and removes the
// older ones, so only the latest is kept.
func newScratchDir(dataDir, id string) (string, error) {
	if dataDir == "" {
		return "", errors.New("no data dir")
	}
	root := preflightRoot(dataDir)
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, id)
	// The copied files may hold secrets.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// WriteReport writes r to <data dir>/.panel-update/preflight/<id>/report.json
// (0600) and removes every other report directory.
func WriteReport(dataDir string, r *Report) error {
	if !ValidReportID(r.ID) {
		return fmt.Errorf("invalid report id %q", r.ID)
	}
	root := preflightRoot(dataDir)
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.Name() != r.ID {
				if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	dir := filepath.Join(root, r.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".report-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmp.Name(), filepath.Join(dir, reportFile))
}

// ReadReport reads the report with the given id; fs.ErrNotExist when there is
// none (a later Preflight removes it).
func ReadReport(dataDir, id string) (*Report, error) {
	if !ValidReportID(id) {
		return nil, fmt.Errorf("invalid report id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(preflightRoot(dataDir), id, reportFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no report %s (a later preflight replaced it): %w", id, err)
		}
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report %s: %w", id, err)
	}
	return &r, nil
}
