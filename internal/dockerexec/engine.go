package dockerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"relist-panel/internal/composes"
)

// Engine is the real composes.ContainerEngine: the Engine API over the Docker
// socket, for the per-Container calls a Rollout needs (ADR-0023), which
// `docker compose` can't address by ID.
type Engine struct {
	client *http.Client
}

func NewEngine(socketPath string) *Engine {
	return &Engine{client: newSocketClient(socketPath)}
}

func (e *Engine) ProjectContainers(ctx context.Context, project string) ([]composes.EngineContainer, error) {
	filters, err := json.Marshal(map[string][]string{"label": {
		"com.docker.compose.project=" + project,
		"com.docker.compose.oneoff=False",
	}})
	if err != nil {
		return nil, err
	}
	var listed []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		State  string            `json:"State"`
		Labels map[string]string `json:"Labels"`
	}
	path := "/containers/json?all=1&filters=" + url.QueryEscape(string(filters))
	if err := e.do(ctx, http.MethodGet, path, &listed); err != nil {
		return nil, err
	}
	containers := make([]composes.EngineContainer, 0, len(listed))
	for _, c := range listed {
		container := composes.EngineContainer{ID: c.ID, Service: c.Labels["com.docker.compose.service"], Running: c.State == "running"}
		if len(c.Names) > 0 {
			container.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		containers = append(containers, container)
	}
	return containers, nil
}

func (e *Engine) Inspect(ctx context.Context, id string) (composes.EngineState, error) {
	var inspected struct {
		State struct {
			Running bool `json:"Running"`
			Health  *struct {
				Status string `json:"Status"`
				Log    []struct {
					Output string `json:"Output"`
				} `json:"Log"`
			} `json:"Health"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		Mounts []struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Destination string `json:"Destination"`
		} `json:"Mounts"`
	}
	if err := e.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", &inspected); err != nil {
		return composes.EngineState{}, err
	}
	state := composes.EngineState{Running: inspected.State.Running, Labels: inspected.Config.Labels}
	if h := inspected.State.Health; h != nil {
		state.Health = h.Status
		if len(h.Log) > 0 {
			state.LastHealthOutput = h.Log[len(h.Log)-1].Output
		}
	}
	for _, m := range inspected.Mounts {
		if m.Type != "volume" {
			continue
		}
		var volume struct {
			Labels map[string]string `json:"Labels"`
		}
		if err := e.do(ctx, http.MethodGet, "/volumes/"+url.PathEscape(m.Name), &volume); err != nil {
			return composes.EngineState{}, err
		}
		_, anonymous := volume.Labels["com.docker.volume.anonymous"]
		state.Mounts = append(state.Mounts, composes.EngineMount{Destination: m.Destination, Anonymous: anonymous})
	}
	return state, nil
}

// Stop sends no t, so Docker waits the Container's own StopTimeout, which
// Compose sets from stop_grace_period.
func (e *Engine) Stop(ctx context.Context, id string) error {
	return e.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop?", nil)
}

func (e *Engine) Remove(ctx context.Context, id string, force, volumes bool) error {
	path := fmt.Sprintf("/containers/%s?force=%d&v=%d", url.PathEscape(id), flag(force), flag(volumes))
	err := e.do(ctx, http.MethodDelete, path, nil)
	if apiErr, ok := err.(*engineError); ok && apiErr.status == http.StatusNotFound {
		return nil
	}
	return err
}

func flag(on bool) int {
	if on {
		return 1
	}
	return 0
}

type engineError struct {
	status int
	msg    string
}

func (e *engineError) Error() string { return e.msg }

// do sends one request and decodes a JSON body into out when out is set.
func (e *Engine) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
	if err != nil {
		return fmt.Errorf("docker api %s: %w", path, err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &engineError{status: resp.StatusCode, msg: fmt.Sprintf("docker api %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(body)))}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	return nil
}
