package panelupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// networkName is the shared network the Updater joins and Traefik is reached
// on.
const networkName = "relist-network"

// Files of a run directory the panel writes for the Updater.
const (
	planFile          = "plan.json"
	candidateInstall  = "candidate-install"
	candidateEnvFile  = "candidate-env"
	panelServiceName  = "panel"
	traefikServiceKey = "traefik"
)

// Plan is everything the Updater needs, written by the panel into the run
// directory before the Updater starts. It is why the Updater has one flag,
// --run: nothing it does depends on the Preflight's scratch directory.
type Plan struct {
	Run         string   `json:"run"`
	Project     string   `json:"project"`
	InstallDir  string   `json:"installDir"`
	ConfigFiles []string `json:"configFiles"`
	// Domain is RELIST_DOMAIN, the Host and SNI of the health request.
	Domain string `json:"domain"`
	// TargetRef is the manifest's digest reference; TargetImageID is what the
	// Engine says that reference is, which a Container's Image field matches.
	TargetRef     string `json:"targetRef"`
	TargetImageID string `json:"targetImageId"`
	// FromImageID is the image the panel ran from, which a rollback waits for.
	FromImageID string `json:"fromImageId"`
}

// EnvFile is the Install's .env.
func (p Plan) EnvFile() string { return filepath.Join(p.InstallDir, ".env") }

func writePlan(dir string, p Plan, install, env []byte) error {
	if err := os.WriteFile(filepath.Join(dir, candidateInstall), install, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, candidateEnvFile), env, 0o600); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, planFile), data, 0o600)
}

// ReadPlan reads a run directory's plan.json.
func ReadPlan(dir string) (Plan, error) {
	var p Plan
	data, err := os.ReadFile(filepath.Join(dir, planFile))
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(data, &p)
}

// Validate checks a plan the Updater is about to act on with the Docker socket
// in hand: it must name this run, and every path must be absolute, clean and
// where the panel would have put it.
func (p Plan) Validate(run, dataDir string) error {
	if p.Run != run {
		return fmt.Errorf("plan.json is for run %q, not %q", p.Run, run)
	}
	for name, v := range map[string]string{
		"project": p.Project, "domain": p.Domain, "targetRef": p.TargetRef,
		"targetImageId": p.TargetImageID, "fromImageId": p.FromImageID,
	} {
		if v == "" {
			return fmt.Errorf("plan.json has no %s", name)
		}
	}
	if !cleanAbs(dataDir) {
		return fmt.Errorf("the data directory %q is not an absolute, clean path", dataDir)
	}
	if !cleanAbs(p.InstallDir) {
		return fmt.Errorf("the Install directory %q is not an absolute, clean path", p.InstallDir)
	}
	if len(p.ConfigFiles) == 0 {
		return errors.New("plan.json lists no config files")
	}
	for _, f := range p.ConfigFiles {
		if !cleanAbs(f) {
			return fmt.Errorf("the config file %q is not an absolute, clean path", f)
		}
		if !strings.HasPrefix(f, p.InstallDir+string(filepath.Separator)) {
			return fmt.Errorf("the config file %q is outside the Install directory %q", f, p.InstallDir)
		}
	}
	return nil
}

func cleanAbs(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}
