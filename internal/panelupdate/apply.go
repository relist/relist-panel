package panelupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Refusal is an Apply the panel will not start, in words for the operator.
// The route answers it with 409.
type Refusal struct{ Message string }

func (r *Refusal) Error() string { return r.Message }

func refuse(format string, args ...any) error {
	return &Refusal{Message: fmt.Sprintf(format, args...)}
}

// Apply is the spec's Apply, steps 1 to 3. It re-runs the Preflight and
// refuses unless the result is the one the operator confirmed, quiesces and
// backs up, writes plan.json and starts the Updater. It returns the run id;
// the Updater does the rest.
//
// The Preflight's lock is held from the re-run to the start of the Updater, so
// a Preflight started in between cannot remove the scratch directory.
func (s *Service) Apply(ctx context.Context, reportID string, stepsDone []string) (string, error) {
	m, err := s.Available(ctx)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", refuse("No update is available")
	}
	if missing := unticked(m.ManualSteps, stepsDone); len(missing) > 0 {
		return "", refuse("Tick every manual step before updating: %s", strings.Join(missing, ", "))
	}
	s.pfRun.Lock()
	defer s.pfRun.Unlock()
	s.pfMu.Lock()
	confirmed, confirmedFP := s.latest, s.latestFP
	s.pfMu.Unlock()
	if confirmed == nil || confirmed.ID != reportID {
		return "", refuse("The preview is out of date. Run the preview again.")
	}
	if s.d.Updater == nil || s.d.Docker == nil {
		return "", errors.New("this panel cannot start an Updater")
	}
	if s.d.Domain == "" {
		return "", errors.New("RELIST_DOMAIN is not known, so the health check has no host name")
	}
	report, cand, err := s.preflightLocked(ctx, m)
	if err != nil {
		return "", err
	}
	switch {
	case report.Failed != nil:
		return "", refuse("%s", report.Failed.Message)
	case report.NeedsManualUpdate:
		return "", refuse("This version needs a manual update. See %s", ReadmeURL)
	case !report.Ready:
		return "", refuse("The Install does not meet this version's requirements. Run the preview again.")
	case cand.fingerprint != confirmedFP:
		return "", refuse("The Install or the release changed since the preview. Run the preview again.")
	}
	if err := s.clearStaleUpdaters(ctx); err != nil {
		return "", err
	}
	targetID, err := s.d.Docker.ImageID(ctx, m.Image)
	if err != nil {
		return "", fmt.Errorf("find the pulled image %s: %w", m.Image, err)
	}
	run, err := s.QuiesceAndBackup(ctx, cand.in, s.d.Version, m.Version)
	switch {
	case errors.Is(err, ErrUpdateRunning), errors.Is(err, ErrMigrationLocked):
		return "", refuse("%s", err.Error())
	case err != nil:
		return "", err
	}
	if err := s.backupMatches(run, cand); err != nil {
		s.AbortRun(run, err.Error())
		return "", refuse("The Install's files changed while the panel was quiescing. Run the preview again.")
	}
	plan := Plan{
		Run: run.ID, Project: cand.in.Project, InstallDir: cand.in.Dir, ConfigFiles: cand.in.ConfigFiles,
		Domain: s.d.Domain, TargetRef: m.Image, TargetImageID: targetID, FromImageID: cand.in.Image,
	}
	if err := writePlan(run.Dir, plan, cand.install, cand.env); err != nil {
		s.AbortRun(run, "write plan.json: "+err.Error())
		return "", fmt.Errorf("write the plan: %w", err)
	}
	if err := s.SetRunState(run, StateStartingUpdater, ""); err != nil {
		s.AbortRun(run, err.Error())
		return "", err
	}
	err = s.d.Updater.StartUpdater(ctx, UpdaterSpec{
		Name: UpdaterName(run.ID), Image: cand.in.Image, Run: run.ID,
		InstallDir: cand.in.Dir, DataDir: s.d.DataDir, Network: networkName,
	})
	if err != nil {
		s.AbortRun(run, "start the Updater: "+err.Error())
		return "", fmt.Errorf("start the Updater: %w", err)
	}
	s.MarkStarted(run)
	return run.ID, nil
}

// unticked lists the manual steps the operator did not confirm.
func unticked(steps []ManualStep, done []string) []string {
	have := map[string]bool{}
	for _, id := range done {
		have[id] = true
	}
	var missing []string
	for _, st := range steps {
		if !have[st.ID] {
			missing = append(missing, st.ID)
		}
	}
	return missing
}

// clearStaleUpdaters refuses while an Updater of any run still runs, and
// removes the ones that have exited.
func (s *Service) clearStaleUpdaters(ctx context.Context) error {
	found, err := s.d.Updater.ListUpdaters(ctx)
	if err != nil {
		return fmt.Errorf("list the Updater Containers: %w", err)
	}
	for _, u := range found {
		if u.Running {
			return refuse("The Updater %s is still running. Wait for it, or remove it with docker rm -f %s.", u.Name, u.Name)
		}
	}
	for _, u := range found {
		if err := s.d.Updater.RemoveUpdater(ctx, u.Name); err != nil {
			return fmt.Errorf("remove the old Updater %s: %w", u.Name, err)
		}
	}
	return nil
}

// backupMatches checks the files the backup took are the ones the Preflight
// read, so the update replaces what the operator was shown.
func (s *Service) backupMatches(run Run, c *candidate) error {
	b, err := ReadBackup(run.Dir)
	if err != nil {
		return err
	}
	if len(b.Config) != len(c.files.Config) {
		return errors.New("the Install files changed")
	}
	for i, f := range b.Config {
		data, err := os.ReadFile(filepath.Join(run.Dir, f.Copy))
		if err != nil {
			return err
		}
		if !bytes.Equal(data, c.files.Config[i]) {
			return fmt.Errorf("%s changed after the preview", f.Path)
		}
	}
	var env []byte
	if b.Env != nil {
		if env, err = os.ReadFile(filepath.Join(run.Dir, b.Env.Copy)); err != nil {
			return err
		}
	}
	if !bytes.Equal(env, c.files.Env) {
		return errors.New(".env changed after the preview")
	}
	return nil
}

// OwnerName is who a prune plan says the previous panel image belongs to.
const OwnerName = "Panel update"

// PreviousImage is the image ID the panel ran from before its last successful
// update. A prune plan never offers it: it is what a manual rollback runs.
func (s *Service) PreviousImage(ctx context.Context) (string, error) {
	return s.d.Store.PreviousImage(ctx)
}
