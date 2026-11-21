package dockerexec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/databases"
)

// DatabaseHost implements databases.Host over the Engine API (ADR-0039).
type DatabaseHost struct {
	engine *Engine
}

func NewDatabaseHost(socketPath string) *DatabaseHost {
	return &DatabaseHost{engine: NewEngine(socketPath)}
}

var _ databases.Host = (*DatabaseHost)(nil)

// stopGrace is how long Docker waits for a database to flush before killing it.
const stopGrace = 30

type dbHealthcheck struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval"`
	Timeout     int64    `json:"Timeout"`
	Retries     int      `json:"Retries"`
	StartPeriod int64    `json:"StartPeriod"`
}

type dbLogConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config"`
}

type dbHostConfig struct {
	Binds         []string      `json:"Binds"`
	NetworkMode   string        `json:"NetworkMode"`
	RestartPolicy restartPolicy `json:"RestartPolicy"`
	Memory        int64         `json:"Memory,omitempty"`
	NanoCpus      int64         `json:"NanoCpus,omitempty"`
	LogConfig     dbLogConfig   `json:"LogConfig"`
}

type dbEndpoint struct {
	Aliases []string `json:"Aliases,omitempty"`
}

type dbCreate struct {
	Image            string            `json:"Image"`
	Env              []string          `json:"Env"`
	Cmd              []string          `json:"Cmd,omitempty"`
	Labels           map[string]string `json:"Labels"`
	Healthcheck      dbHealthcheck     `json:"Healthcheck"`
	HostConfig       dbHostConfig      `json:"HostConfig"`
	NetworkingConfig struct {
		EndpointsConfig map[string]dbEndpoint `json:"EndpointsConfig"`
	} `json:"NetworkingConfig"`
}

func (h *DatabaseHost) EnsureImage(ctx context.Context, ref string) error {
	if _, err := h.ImageID(ctx, ref); err == nil {
		return nil
	} else if !isEngineNotFound(err) {
		return err
	}
	return h.pull(ctx, ref)
}

func (h *DatabaseHost) pull(ctx context.Context, ref string) error {
	name, tag := ref, ""
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	q := url.Values{"fromImage": {name}}
	if tag != "" {
		q.Set("tag", tag)
	}
	return h.engine.stream(ctx, "/images/create?"+q.Encode())
}

func (h *DatabaseHost) PullImage(ctx context.Context, ref string) (string, error) {
	if err := h.pull(ctx, ref); err != nil {
		return "", err
	}
	return h.ImageID(ctx, ref)
}

func (h *DatabaseHost) ImageID(ctx context.Context, ref string) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := h.engine.do(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (h *DatabaseHost) Inspect(ctx context.Context, name string) (databases.ContainerState, error) {
	var out struct {
		State struct {
			Running bool `json:"Running"`
			Health  *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		Image  string `json:"Image"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]struct {
				Aliases []string `json:"Aliases"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	err := h.engine.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", &out)
	if isEngineNotFound(err) {
		return databases.ContainerState{}, nil
	}
	if err != nil {
		return databases.ContainerState{}, err
	}
	st := databases.ContainerState{
		Exists: true, Running: out.State.Running,
		Hash: out.Config.Labels[databases.HashLabel], ImageID: out.Image,
		Aliases: out.NetworkSettings.Networks[databases.Network].Aliases,
	}
	if out.State.Health != nil {
		st.Health = out.State.Health.Status
	}
	return st, nil
}

func (h *DatabaseHost) VolumeExists(ctx context.Context, name string) (bool, error) {
	err := h.engine.do(ctx, http.MethodGet, "/volumes/"+url.PathEscape(name), nil)
	if isEngineNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (h *DatabaseHost) Create(ctx context.Context, spec databases.ContainerSpec) error {
	// A recreate (new image, new settings) must let the server flush first:
	// the forced remove alone would SIGKILL it, and Redis would lose what it
	// wrote since its last snapshot. A stopped container answers 304.
	if err := h.Stop(ctx, spec.Name); err != nil && !isEngineNotFound(err) {
		return err
	}
	if err := h.engine.Remove(ctx, spec.Name, true, false); err != nil {
		return err
	}
	if ok, err := h.VolumeExists(ctx, spec.Volume); err != nil {
		return err
	} else if !ok {
		vol := map[string]any{"Name": spec.Volume, "Labels": map[string]string{databases.IDLabel: spec.Labels[databases.IDLabel]}}
		if err := h.engine.doBody(ctx, http.MethodPost, "/volumes/create", vol, nil); err != nil {
			return err
		}
	}
	body := dbCreate{
		Image: spec.Image, Env: spec.Env, Cmd: spec.Cmd, Labels: spec.Labels,
		Healthcheck: dbHealthcheck{
			Test: spec.Healthcheck, Interval: int64(10 * time.Second), Timeout: int64(5 * time.Second),
			Retries: 5, StartPeriod: int64(30 * time.Second),
		},
		HostConfig: dbHostConfig{
			Binds:         []string{spec.Volume + ":" + spec.DataPath},
			NetworkMode:   databases.Network,
			RestartPolicy: restartPolicy{Name: "unless-stopped"},
			Memory:        spec.MemoryBytes, NanoCpus: spec.NanoCPUs,
			LogConfig: dbLogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "5"}},
		},
	}
	ep := dbEndpoint{}
	if spec.Alias != "" {
		ep.Aliases = []string{spec.Alias}
	}
	body.NetworkingConfig.EndpointsConfig = map[string]dbEndpoint{databases.Network: ep}
	if err := h.engine.doBody(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(spec.Name), body, nil); err != nil {
		return err
	}
	return h.Start(ctx, spec.Name)
}

func (h *DatabaseHost) action(ctx context.Context, name, verb string) error {
	return h.engine.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/"+verb, nil)
}

func (h *DatabaseHost) Start(ctx context.Context, name string) error {
	return h.action(ctx, name, "start")
}
func (h *DatabaseHost) Stop(ctx context.Context, name string) error {
	return h.action(ctx, name, "stop?t="+strconv.Itoa(stopGrace))
}
func (h *DatabaseHost) Restart(ctx context.Context, name string) error {
	return h.action(ctx, name, "restart?t="+strconv.Itoa(stopGrace))
}

func (h *DatabaseHost) Realias(ctx context.Context, name, alias string) error {
	path := "/networks/" + databases.Network
	if err := h.engine.doBody(ctx, http.MethodPost, path+"/disconnect", map[string]any{"Container": name, "Force": true}, nil); err != nil {
		return err
	}
	ep := dbEndpoint{}
	if alias != "" {
		ep.Aliases = []string{alias}
	}
	return h.engine.doBody(ctx, http.MethodPost, path+"/connect", map[string]any{"Container": name, "EndpointConfig": ep}, nil)
}

func (h *DatabaseHost) Remove(ctx context.Context, name string) error {
	return h.engine.Remove(ctx, name, true, false)
}

func (h *DatabaseHost) RemoveVolume(ctx context.Context, name string) error {
	err := h.engine.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), nil)
	if isEngineNotFound(err) {
		return nil
	}
	return err
}

func (h *DatabaseHost) Logs(ctx context.Context, name string, tail int) (string, error) {
	path := fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&timestamps=1&tail=%d", url.PathEscape(name), tail)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.engine.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker api GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("docker api GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return demux(raw), nil
}
