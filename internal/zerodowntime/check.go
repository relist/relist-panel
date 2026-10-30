package zerodowntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Problem is why one Service can't be rolled out.
type Problem struct {
	Service string   `json:"service"`
	Reasons []string `json:"reasons"`
}

// config is the part of `docker compose config --format json` the checks read.
// Its values are already interpolated, so ${VAR} never reaches a check.
type config struct {
	Services map[string]service `json:"services"`
}

type service struct {
	ContainerName string       `json:"container_name"`
	Healthcheck   *healthcheck `json:"healthcheck"`
	Ports         []port       `json:"ports"`
	Deploy        *struct {
		Replicas *int `json:"replicas"`
	} `json:"deploy"`
	Scale   *int              `json:"scale"`
	Volumes []volume          `json:"volumes"`
	Labels  map[string]string `json:"labels"`
}

type healthcheck struct {
	Test        []string `json:"test"`
	Disable     bool     `json:"disable"`
	Interval    string   `json:"interval"`
	Timeout     string   `json:"timeout"`
	Retries     *int     `json:"retries"`
	StartPeriod string   `json:"start_period"`
}

type port struct {
	Published string `json:"published"`
}

type volume struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

func parse(configJSON []byte) (config, error) {
	var c config
	if err := json.Unmarshal(configJSON, &c); err != nil {
		return config{}, fmt.Errorf("read compose config: %w", err)
	}
	return c, nil
}

// Check returns, for each of services, why it can't roll out; Services with no
// reason, and names the config doesn't have, are left out.
func Check(configJSON []byte, services []string) ([]Problem, error) {
	c, err := parse(configJSON)
	if err != nil {
		return nil, err
	}
	var problems []Problem
	for _, name := range services {
		svc, ok := c.Services[name]
		if !ok {
			continue
		}
		if reasons := svc.reasons(); len(reasons) > 0 {
			problems = append(problems, Problem{Service: name, Reasons: reasons})
		}
	}
	return problems, nil
}

func (s service) reasons() []string {
	var reasons []string
	if !s.Healthcheck.enabled() {
		reasons = append(reasons, "has no healthcheck")
	}
	if s.ContainerName != "" {
		reasons = append(reasons, "sets container_name; two Containers can't share a name")
	}
	for _, p := range s.Ports {
		if p.Published != "" {
			reasons = append(reasons, fmt.Sprintf("publishes host port %s; two Containers can't bind it", p.Published))
		}
	}
	if (s.Deploy != nil && s.Deploy.Replicas != nil && *s.Deploy.Replicas > 1) || (s.Scale != nil && *s.Scale > 1) {
		reasons = append(reasons, "runs more than one replica; rollout supports one")
	}
	for _, v := range s.Volumes {
		if v.Type == "volume" && v.Source == "" {
			reasons = append(reasons, fmt.Sprintf("has an anonymous volume at %s; mount a named volume there", v.Target))
		}
	}
	return reasons
}

func (h *healthcheck) enabled() bool {
	return h != nil && !h.Disable && len(h.Test) > 0 && h.Test[0] != "NONE"
}

// TraefikLabels returns service's labels whose key starts with "traefik.",
// with compose config's $$ turned back into the $ a running Container holds.
func TraefikLabels(configJSON []byte, service string) (map[string]string, error) {
	c, err := parse(configJSON)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for k, v := range c.Services[service].Labels {
		if strings.HasPrefix(k, "traefik.") {
			labels[k] = strings.ReplaceAll(v, "$$", "$")
		}
	}
	return labels, nil
}

// Docker's healthcheck defaults, for fields the Compose file leaves unset.
const (
	defaultInterval = 30 * time.Second
	defaultTimeout  = 30 * time.Second
	defaultRetries  = 3
	waitMargin      = 10 * time.Second
)

// WaitLimit is how long a new Container may take to become healthy:
// start_period + (interval + timeout) × retries + 10s.
func WaitLimit(configJSON []byte, service string) (time.Duration, error) {
	c, err := parse(configJSON)
	if err != nil {
		return 0, err
	}
	h := c.Services[service].Healthcheck
	if h == nil {
		h = &healthcheck{}
	}
	duration := func(value string, fallback time.Duration) (time.Duration, error) {
		if value == "" {
			return fallback, nil
		}
		return time.ParseDuration(value)
	}
	interval, err := duration(h.Interval, defaultInterval)
	if err != nil {
		return 0, err
	}
	timeout, err := duration(h.Timeout, defaultTimeout)
	if err != nil {
		return 0, err
	}
	start, err := duration(h.StartPeriod, 0)
	if err != nil {
		return 0, err
	}
	retries := defaultRetries
	if h.Retries != nil {
		retries = *h.Retries
	}
	return start + (interval+timeout)*time.Duration(retries) + waitMargin, nil
}
