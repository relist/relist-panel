package panelupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Files of a run directory the panel writes for the Updater.
const (
	planFile         = "plan.json"
	candidateInstall = "candidate-install"
	candidateEnvFile = "candidate-env"
)

// Plan is what Service.Apply still writes for the old Updater. The Updater no
// longer reads it; Task 9 deletes this file with Service.Apply.
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
