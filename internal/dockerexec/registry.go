package dockerexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"relist-panel/internal/registry"
)

// RegistryHost implements registry.Host over the Engine API (ADR-0033).
type RegistryHost struct {
	engine *Engine
}

func NewRegistryHost(socketPath string) *RegistryHost {
	return &RegistryHost{engine: NewEngine(socketPath)}
}

var _ registry.Host = (*RegistryHost)(nil)

const registryRouter = "relist-registry"

// registryLabels route https://<hostname> to the registry on websecure, and
// redirect http to it, like a Domain's labels do.
func registryLabels(spec registry.ContainerSpec) map[string]string {
	r := "traefik.http.routers." + registryRouter
	h := "traefik.http.routers." + registryRouter + "-http"
	rule := "Host(`" + spec.Hostname + "`)"
	labels := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": registry.Network,
		r + ".rule":              rule,
		r + ".entrypoints":       "websecure",
		r + ".service":           registryRouter,
		h + ".rule":              rule,
		h + ".entrypoints":       "web",
		h + ".middlewares":       registryRouter + "-redirect",
		h + ".service":           registryRouter,
		"traefik.http.middlewares." + registryRouter + "-redirect.redirectscheme.scheme":    "https",
		"traefik.http.middlewares." + registryRouter + "-redirect.redirectscheme.permanent": "true",
		"traefik.http.services." + registryRouter + ".loadbalancer.server.port":             strconv.Itoa(registry.Port),
		registry.HashLabel: spec.Hash,
	}
	if spec.TLS == registry.TLSLetsEncrypt {
		labels[r+".tls.certresolver"] = "letsencrypt"
	} else {
		labels[r+".tls"] = "true"
	}
	return labels
}

func registryBinds(filesDir string) []string {
	return []string{registry.VolumeName + ":/var/lib/registry", filesDir + ":/etc/distribution:ro"}
}

type restartPolicy struct {
	Name string `json:"Name"`
}

type hostConfig struct {
	Binds         []string      `json:"Binds"`
	NetworkMode   string        `json:"NetworkMode"`
	RestartPolicy restartPolicy `json:"RestartPolicy"`
}

type containerCreate struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Labels     map[string]string `json:"Labels"`
	HostConfig hostConfig        `json:"HostConfig"`
}

// EnsureImage pulls ref unless it is already on the host.
func (h *RegistryHost) EnsureImage(ctx context.Context, ref string) error {
	err := h.engine.do(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil)
	var apiErr *engineError
	if err == nil {
		return nil
	}
	if !errors.As(err, &apiErr) || apiErr.status != http.StatusNotFound {
		return err
	}
	name, digest, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i] // a digest pins the image; the tag only names it
	}
	q := url.Values{"fromImage": {name}}
	if digest != "" {
		q.Set("tag", digest)
	}
	return h.engine.stream(ctx, "/images/create?"+q.Encode())
}

func (h *RegistryHost) Inspect(ctx context.Context) (registry.ContainerState, error) {
	var out struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	err := h.engine.do(ctx, http.MethodGet, "/containers/"+registry.ContainerName+"/json", &out)
	if isEngineNotFound(err) {
		return registry.ContainerState{}, nil
	}
	if err != nil {
		return registry.ContainerState{}, err
	}
	return registry.ContainerState{Exists: true, Running: out.State.Running, Image: out.Config.Image, Hash: out.Config.Labels[registry.HashLabel]}, nil
}

func (h *RegistryHost) Create(ctx context.Context, spec registry.ContainerSpec) error {
	if err := h.engine.Remove(ctx, registry.ContainerName, true, false); err != nil {
		return err
	}
	body := containerCreate{
		Image:  spec.Image,
		Labels: registryLabels(spec),
		HostConfig: hostConfig{
			Binds:         registryBinds(spec.FilesDir),
			NetworkMode:   registry.Network,
			RestartPolicy: restartPolicy{Name: "unless-stopped"},
		},
	}
	if err := h.engine.doBody(ctx, http.MethodPost, "/containers/create?name="+registry.ContainerName, body, nil); err != nil {
		return err
	}
	return h.engine.do(ctx, http.MethodPost, "/containers/"+registry.ContainerName+"/start", nil)
}

func (h *RegistryHost) Restart(ctx context.Context) error {
	return h.engine.do(ctx, http.MethodPost, "/containers/"+registry.ContainerName+"/restart?t=10", nil)
}

func (h *RegistryHost) Remove(ctx context.Context) error {
	return h.engine.Remove(ctx, registry.ContainerName, true, false)
}

func (h *RegistryHost) RemoveVolume(ctx context.Context) error {
	err := h.engine.do(ctx, http.MethodDelete, "/volumes/"+registry.VolumeName, nil)
	if isEngineNotFound(err) {
		return nil
	}
	return err
}

func (h *RegistryHost) VolumeBytes(ctx context.Context) (int64, error) {
	var out struct {
		Volumes []struct {
			Name      string `json:"Name"`
			UsageData struct {
				Size int64 `json:"Size"`
			} `json:"UsageData"`
		} `json:"Volumes"`
	}
	if err := h.engine.do(ctx, http.MethodGet, "/system/df?type=volume", &out); err != nil {
		return 0, err
	}
	for _, v := range out.Volumes {
		if v.Name == registry.VolumeName {
			return max(v.UsageData.Size, 0), nil
		}
	}
	return 0, nil
}

// RunGC starts garbage collection in its own container, detached and with no
// network, on the registry's volume and config. It is not removed on exit, so
// the panel can read its exit code and log, even after a restart.
func (h *RegistryHost) RunGC(ctx context.Context, image, filesDir string) error {
	if err := h.RemoveGC(ctx); err != nil {
		return err
	}
	body := containerCreate{
		Image:      image,
		Cmd:        []string{"garbage-collect", "--delete-untagged", "/etc/distribution/config.yml"},
		Labels:     map[string]string{},
		HostConfig: hostConfig{Binds: registryBinds(filesDir), NetworkMode: "none", RestartPolicy: restartPolicy{Name: "no"}},
	}
	if err := h.engine.doBody(ctx, http.MethodPost, "/containers/create?name="+registry.GCContainerName, body, nil); err != nil {
		return err
	}
	return h.engine.do(ctx, http.MethodPost, "/containers/"+registry.GCContainerName+"/start", nil)
}

func (h *RegistryHost) GCState(ctx context.Context) (registry.GCState, error) {
	var out struct {
		State struct {
			Running  bool `json:"Running"`
			ExitCode int  `json:"ExitCode"`
		} `json:"State"`
	}
	err := h.engine.do(ctx, http.MethodGet, "/containers/"+registry.GCContainerName+"/json", &out)
	if isEngineNotFound(err) {
		return registry.GCState{}, nil
	}
	if err != nil {
		return registry.GCState{}, err
	}
	logs, err := h.logs(ctx, registry.GCContainerName)
	if err != nil {
		return registry.GCState{}, err
	}
	return registry.GCState{Exists: true, Running: out.State.Running, ExitCode: out.State.ExitCode, Logs: logs}, nil
}

// logs is a container's last 200 lines of stdout and stderr, demultiplexed.
func (h *RegistryHost) logs(ctx context.Context, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+name+"/logs?stdout=1&stderr=1&tail=200", nil)
	if err != nil {
		return "", err
	}
	resp, err := h.engine.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return demux(raw), nil
}

// demux strips the Engine's 8-byte stream headers; a TTY stream has none.
func demux(raw []byte) string {
	var out bytes.Buffer
	for len(raw) >= 8 && (raw[0] == 1 || raw[0] == 2) && raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
		n := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
		raw = raw[8:]
		if n > len(raw) {
			n = len(raw)
		}
		out.Write(raw[:n])
		raw = raw[n:]
	}
	out.Write(raw)
	return out.String()
}

func (h *RegistryHost) RemoveGC(ctx context.Context) error {
	return h.engine.Remove(ctx, registry.GCContainerName, true, false)
}

func isEngineNotFound(err error) bool {
	var apiErr *engineError
	return errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound
}
