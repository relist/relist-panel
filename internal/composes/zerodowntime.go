package composes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/domains"
	"relist-panel/internal/zerodowntime"
)

// zeroDowntimeServices is the enabled Services that are in the YAML and have
// a Domain, in YAML order (ADR-0023). Other switches are ignored.
func zeroDowntimeServices(yamlContent string, ds []domains.Domain, enabled []string) ([]string, error) {
	if len(enabled) == 0 {
		return nil, nil
	}
	on := map[string]bool{}
	for _, name := range enabled {
		on[name] = true
	}
	hasDomain := map[string]bool{}
	for _, d := range ds {
		hasDomain[d.Service] = true
	}
	names, err := serviceNames(yamlContent)
	if err != nil {
		return nil, err
	}
	var services []string
	for _, name := range names {
		if on[name] && hasDomain[name] {
			services = append(services, name)
		}
	}
	return services, nil
}

// serviceNames is every key of the YAML's services, in file order.
func serviceNames(yamlContent string) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || resolveAlias(doc.Content[0]).Kind != yaml.MappingNode {
		return nil, nil
	}
	root, err := orderedMappingEntries(doc.Content[0])
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range root {
		if resolveAlias(entry.key).Value != "services" {
			continue
		}
		services, err := orderedMappingEntries(entry.value)
		if err != nil {
			return nil, err
		}
		for _, service := range services {
			names = append(names, resolveAlias(service.key).Value)
		}
	}
	return names, nil
}

// stageCheck writes the snapshot's files into the Compose dir under names no
// other run uses, so relative env_file, include and extends paths resolve as
// on deploy and a running build's .build-* files are left alone.
func stageCheck(t DeployTarget) (RunRequest, func(), error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return RunRequest{}, nil, err
	}
	base := filepath.Join(ComposeDir(t.Request), ".zd-check-"+hex.EncodeToString(suffix[:]))
	return stageSnapshot(t, base+".yml", base+".env", base+"-sets")
}

// ZeroDowntimeProblems checks the snapshot's zero-downtime Services against
// `docker compose config` of the snapshot's own files. An error from Compose
// itself (a bad interpolation, say) is returned as is.
func (s *Service) ZeroDowntimeProblems(ctx context.Context, t DeployTarget) ([]zerodowntime.Problem, error) {
	if t.Snapshot == nil || len(t.Snapshot.ZeroDowntime) == 0 {
		return nil, nil
	}
	req, cleanup, err := stageCheck(t)
	if err != nil {
		return nil, fmt.Errorf("could not stage the zero-downtime check: %w", err)
	}
	defer cleanup()
	config, err := s.runner.Config(ctx, req)
	if err != nil {
		// Compose refusing the file is the operator's to fix, as a bad YAML is.
		return nil, failure(FailureInvalid, "compose config failed: "+firstErrorLine(err), err)
	}
	return zerodowntime.Check(config, t.Snapshot.ZeroDowntime)
}

// MarkDeployed records the deploy time once a Deployment's rollouts are done.
func (s *Service) MarkDeployed(ctx context.Context, t DeployTarget) error {
	return s.repository.markDeployed(ctx, t.Compose.EnvironmentID, t.Compose.ID, s.now().UTC().Truncate(time.Second))
}

// activeServices is the Services `up` would start: the keys of the written
// file's config, which leaves out Services whose profile is off. Naming one
// of those on the command line would start it.
func (s *Service) activeServices(ctx context.Context, req RunRequest, yamlContent string) ([]string, error) {
	raw, err := s.runner.Config(ctx, req)
	if err != nil {
		return nil, err
	}
	var config struct {
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("read compose config: %w", err)
	}
	names, err := serviceNames(yamlContent)
	if err != nil {
		return nil, err
	}
	var active []string
	for _, name := range names {
		if _, ok := config.Services[name]; ok {
			active = append(active, name)
		}
	}
	return active, nil
}

// errNoEngine is a rollout or orphan removal without a Container engine.
var errNoEngine = errors.New("no container engine is configured")

// cleanupTimeout bounds each Engine call that must run even after the Deploy
// step's deadline.
const cleanupTimeout = 30 * time.Second

// removeOrphans stops and removes the project's Containers whose Service is
// no longer in the file, which `up --remove-orphans` would do had any
// Service been left for it to start.
func (s *Service) removeOrphans(ctx context.Context, project, yamlContent string) error {
	if s.engine == nil {
		return errNoEngine
	}
	names, err := serviceNames(yamlContent)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	containers, err := s.engine.ProjectContainers(ctx, project)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if known[c.Service] {
			continue
		}
		// Stopping waits out the orphan's stop_grace_period, as for an old
		// Container in a rollout.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
		err := s.engine.Stop(cctx, c.ID)
		cancel()
		if err == nil {
			cctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			err = s.engine.Remove(cctx, c.ID, false, false)
			cancel()
		}
		if err != nil {
			return fmt.Errorf("remove orphan %s: %w", c.Name, err)
		}
	}
	return nil
}

// firstErrorLine is the line of a failed command's output that says what went
// wrong: dockerexec's "docker <argv>: exit status N: " prefix is dropped, and
// so are the warnings Compose prints before its error.
func firstErrorLine(err error) string {
	text := strings.TrimSpace(err.Error())
	if _, rest, ok := strings.Cut(text, ": exit status "); ok {
		if _, stderr, ok := strings.Cut(rest, ": "); ok {
			text = stderr
		}
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" && !strings.Contains(line, "level=warning") {
			return line
		}
	}
	return strings.TrimSpace(lines[0])
}
