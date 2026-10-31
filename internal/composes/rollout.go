package composes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"relist-panel/internal/zerodowntime"
)

// RolloutOptions tunes RunRollout. Build passes --build to the scale, Deadline
// (when set) caps the health wait, and Out receives the log.
type RolloutOptions struct {
	Build    bool
	Deadline time.Time
	Out      io.Writer
}

var (
	// rolloutPoll is how often a new Container's health is read; tests shorten it.
	rolloutPoll = time.Second
	// rolloutOverlap gives Traefik, which rebuilds its config every 2s, time
	// to add the new Container before the old one stops; tests shorten it.
	rolloutOverlap = 3 * time.Second
)

const (
	// stopTimeout bounds stopping one old Container, its stop_grace_period
	// included.
	stopTimeout = 5 * time.Minute
	// healthOutputLimit is how much of the last health check's output the log
	// keeps.
	healthOutputLimit = 2048
)

// RunRollout replaces service's running Containers with one new Container
// without a gap (ADR-0023): it starts the new one next to them, waits for it
// to be healthy, then stops and removes the old ones. When the new one is not
// healthy in time it is removed and the old ones keep serving. It returns the
// warnings and, on failure, a *DeployFailure naming the Service.
func (s *Service) RunRollout(ctx context.Context, t DeployTarget, service string, opts RolloutOptions) ([]string, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if s.engine == nil {
		return nil, failure(FailureInternal, errNoEngine.Error(), errNoEngine)
	}
	req := t.Request
	config, err := s.runner.Config(ctx, req)
	if err != nil {
		return nil, failure(FailureRunner, "could not read compose config: "+err.Error(), err)
	}
	if active, err := configHasService(config, service); err != nil {
		return nil, failure(FailureInternal, err.Error(), err)
	} else if !active {
		// Naming it in `up` would start a Service whose profile is off.
		fmt.Fprintf(out, "rollout %s: %s is not in the active profiles; skipped\n", service, service)
		return nil, nil
	}
	all, err := s.serviceContainers(ctx, req.ProjectName, service, nil)
	if err != nil {
		return nil, failure(FailureRunner, "could not list containers: "+err.Error(), err)
	}
	var old, stale []EngineContainer
	for _, c := range all {
		if c.Running {
			old = append(old, c)
		} else {
			stale = append(stale, c)
		}
	}
	if len(old) == 0 {
		warning := service + " had no running container; deployed without rollout"
		fmt.Fprintf(out, "warning: %s\n", warning)
		if err := s.runner.Up(ctx, req, UpOptions{Build: opts.Build, Services: []string{service}, Out: opts.Out}); err != nil {
			return nil, failure(FailureRunner, "deploy failed: "+err.Error(), err)
		}
		return []string{warning}, nil
	}

	var oldLabels map[string]string
	for i, c := range old {
		state, err := s.engine.Inspect(ctx, c.ID)
		if err != nil {
			return nil, failure(FailureRunner, "could not inspect "+c.Name+": "+err.Error(), err)
		}
		for _, m := range state.Mounts {
			if m.Anonymous {
				reason := fmt.Sprintf("%s has an anonymous volume at %s (from the image's VOLUME); mount a named volume there or turn off zero-downtime deploy", service, m.Destination)
				return nil, failure(FailureRunner, reason, nil)
			}
		}
		if i == 0 {
			oldLabels = traefikOnly(state.Labels)
		}
	}

	wanted, err := zerodowntime.TraefikLabels(config, service)
	if err != nil {
		return nil, failure(FailureInternal, err.Error(), err)
	}
	limit, err := zerodowntime.WaitLimit(config, service)
	if err != nil {
		return nil, failure(FailureInvalid, "could not read "+service+"'s healthcheck: "+err.Error(), err)
	}
	if !opts.Deadline.IsZero() {
		limit = max(0, min(limit, time.Until(opts.Deadline)))
	}
	var warnings []string
	drift := !maps.Equal(oldLabels, wanted)
	if drift {
		warning := service + "'s Traefik labels changed (Domain edited or zero-downtime just enabled); this deploy has a short outage"
		fmt.Fprintf(out, "warning: %s\n", warning)
		warnings = append(warnings, warning)
	}

	// A stopped Container left by an earlier run counts toward --scale, and
	// --no-recreate would start it instead of a new one from the current file.
	for _, c := range stale {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		err := s.engine.Remove(cctx, c.ID, true, false)
		cancel()
		if err != nil {
			return warnings, failure(FailureRunner, "could not remove the stopped container "+c.Name+": "+err.Error(), err)
		}
		fmt.Fprintf(out, "rollout %s: removed the stopped container %s\n", service, c.Name)
	}

	before := map[string]bool{}
	for _, c := range all {
		before[c.ID] = true
	}
	scaleErr := s.runner.Scale(ctx, req, ScaleOptions{Service: service, Replicas: len(old) + 1, Build: opts.Build, Out: opts.Out})
	// Listed past the deadline too, so whatever the scale created is removed.
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	started, listErr := s.serviceContainers(lctx, req.ProjectName, service, before)
	cancel()
	if scaleErr != nil || listErr != nil || len(started) != 1 {
		s.removeNew(ctx, started)
		switch {
		case scaleErr != nil:
			return warnings, failure(FailureRunner, "deploy failed: "+scaleErr.Error(), scaleErr)
		case listErr != nil:
			return warnings, failure(FailureRunner, "could not list containers: "+listErr.Error(), listErr)
		}
		return warnings, failure(FailureRunner, "could not identify the new container", nil)
	}
	created := started[0]
	fmt.Fprintf(out, "rollout %s: started %s, waiting up to %s for it to be healthy\n", service, created.Name, limit)

	if state, healthy := s.waitHealthy(ctx, created.ID, limit); !healthy {
		if output := state.LastHealthOutput; output != "" {
			if len(output) > healthOutputLimit {
				output = output[:healthOutputLimit]
			}
			fmt.Fprintf(out, "last health check of %s:\n%s\n", created.Name, strings.TrimRight(output, "\n"))
		}
		if err := s.removeNew(ctx, started); err != nil {
			reason := fmt.Sprintf("%s did not become healthy in %s; could not remove the new container %s: %v", service, limit, created.Name, err)
			return warnings, failure(FailureRunner, reason, err)
		}
		return warnings, failure(FailureRunner, fmt.Sprintf("%s did not become healthy in %s; kept the previous container", service, limit), nil)
	}

	if !drift {
		time.Sleep(rolloutOverlap)
	}
	for _, c := range old {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
		err := s.engine.Stop(cctx, c.ID)
		cancel()
		if err == nil {
			cctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			err = s.engine.Remove(cctx, c.ID, false, false)
			cancel()
		}
		if err != nil {
			reason := fmt.Sprintf("%s is serving from %s, but the old container %s could not be removed: %v", service, created.Name, c.Name, err)
			return warnings, failure(FailureRunner, reason, err)
		}
	}
	fmt.Fprintf(out, "rollout %s: %s is healthy; removed the previous container\n", service, created.Name)
	return warnings, nil
}

// serviceContainers lists service's Containers in any state, leaving out skip.
func (s *Service) serviceContainers(ctx context.Context, project, service string, skip map[string]bool) ([]EngineContainer, error) {
	all, err := s.engine.ProjectContainers(ctx, project)
	if err != nil {
		return nil, err
	}
	var containers []EngineContainer
	for _, c := range all {
		if c.Service == service && !skip[c.ID] {
			containers = append(containers, c)
		}
	}
	return containers, nil
}

// waitHealthy reads the Container's health until it is healthy, it fails or
// stops, or limit passes. A limit of zero or less reads nothing.
func (s *Service) waitHealthy(ctx context.Context, id string, limit time.Duration) (EngineState, bool) {
	end := time.Now().Add(limit)
	var state EngineState
	for limit > 0 && ctx.Err() == nil {
		var err error
		state, err = s.engine.Inspect(ctx, id)
		if err == nil {
			switch {
			case state.Health == "healthy":
				return state, true
			case state.Health == "unhealthy" || !state.Running:
				return state, false
			}
		}
		if time.Now().Add(rolloutPoll).After(end) {
			return state, false
		}
		select {
		case <-ctx.Done():
		case <-time.After(rolloutPoll):
		}
	}
	return state, false
}

// removeNew removes Containers a failed rollout started, with their fresh
// anonymous volumes, even after the Deploy step's deadline.
func (s *Service) removeNew(ctx context.Context, containers []EngineContainer) error {
	var first error
	for _, c := range containers {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		if err := s.engine.Remove(cctx, c.ID, true, true); err != nil && first == nil {
			first = err
		}
		cancel()
	}
	return first
}

// configHasService reports whether compose config lists service, which it
// does not when the Service's profile is off.
func configHasService(configJSON []byte, service string) (bool, error) {
	var config struct {
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return false, fmt.Errorf("read compose config: %w", err)
	}
	_, ok := config.Services[service]
	return ok, nil
}

func traefikOnly(labels map[string]string) map[string]string {
	only := map[string]string{}
	for k, v := range labels {
		if strings.HasPrefix(k, "traefik.") {
			only[k] = v
		}
	}
	return only
}
