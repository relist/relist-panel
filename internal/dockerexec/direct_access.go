package dockerexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/databases"
	"relist-panel/internal/directaccess"
)

const (
	proxyEntrypoint = "/usr/local/bin/relist-proxy"
	proxyState      = "/tmp/deadline"
	proxyMemory     = 32 << 20
	proxyPids       = 64
)

// ProxyHost implements directaccess.Host over the Engine API (ADR-0041). It
// runs relist-proxy out of the panel's own image, resolved the way the Volume
// helper's is, and shares its exec plumbing with DatabaseHost.
type ProxyHost struct {
	engine       *Engine
	exec         *DatabaseHost
	resolveImage func(ctx context.Context) (string, error)
}

func NewProxyHost(socketPath string) *ProxyHost {
	return &ProxyHost{engine: NewEngine(socketPath), exec: NewDatabaseHost(socketPath), resolveImage: panelImage}
}

// SetImageResolver replaces how the panel's image is found; tests only.
func (h *ProxyHost) SetImageResolver(f func(context.Context) (string, error)) { h.resolveImage = f }

var _ directaccess.Host = (*ProxyHost)(nil)

type proxyPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type proxyHostConfig struct {
	AutoRemove     bool                          `json:"AutoRemove"`
	RestartPolicy  restartPolicy                 `json:"RestartPolicy"`
	NetworkMode    string                        `json:"NetworkMode"`
	Memory         int64                         `json:"Memory"`
	PidsLimit      int64                         `json:"PidsLimit"`
	CapDrop        []string                      `json:"CapDrop"`
	SecurityOpt    []string                      `json:"SecurityOpt"`
	ReadonlyRootfs bool                          `json:"ReadonlyRootfs"`
	Tmpfs          map[string]string             `json:"Tmpfs"`
	PortBindings   map[string][]proxyPortBinding `json:"PortBindings"`
}

type proxyCreate struct {
	Image        string              `json:"Image"`
	Entrypoint   []string            `json:"Entrypoint"`
	Cmd          []string            `json:"Cmd"`
	Labels       map[string]string   `json:"Labels"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	HostConfig   proxyHostConfig     `json:"HostConfig"`
}

func (h *ProxyHost) StartProxy(ctx context.Context, spec directaccess.ProxySpec) (int, error) {
	image, err := h.resolveImage(ctx)
	if err != nil {
		return 0, fmt.Errorf("direct access: %w", err)
	}
	name := directaccess.ContainerName(spec.AccessID)
	port := strconv.Itoa(spec.ListenPort) + "/tcp"
	hostPort := ""
	if spec.HostPort != 0 {
		hostPort = strconv.Itoa(spec.HostPort)
	}
	deadline := strconv.FormatInt(spec.Deadline.Unix(), 10)
	body := proxyCreate{
		Image:      image,
		Entrypoint: []string{proxyEntrypoint},
		Cmd: []string{"serve", "--listen", ":" + strconv.Itoa(spec.ListenPort), "--target", spec.Upstream,
			"--deadline", deadline, "--state", proxyState},
		Labels: map[string]string{
			directaccess.IDLabel:      strconv.FormatInt(spec.AccessID, 10),
			directaccess.TargetLabel:  spec.TargetLabel,
			directaccess.ExpiresLabel: deadline,
		},
		ExposedPorts: map[string]struct{}{port: {}},
		HostConfig: proxyHostConfig{
			AutoRemove:     true,
			RestartPolicy:  restartPolicy{Name: "no"},
			NetworkMode:    spec.Network,
			Memory:         proxyMemory,
			PidsLimit:      proxyPids,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			ReadonlyRootfs: true,
			Tmpfs:          map[string]string{"/tmp": "size=1m"},
			PortBindings:   map[string][]proxyPortBinding{port: {{HostIP: spec.BindIP, HostPort: hostPort}}},
		},
	}
	if err := h.engine.doBody(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), body, nil); err != nil {
		return 0, err
	}
	// The container is gone on any failure below; AutoRemove only helps once
	// it has run. A fresh context: the caller's may be what failed.
	fail := func(err error) (int, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = h.engine.Remove(cleanup, name, true, false)
		return 0, err
	}
	if err := h.engine.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil); err != nil {
		if spec.HostPort != 0 && portTaken(err) {
			return fail(fmt.Errorf("port %d: %w", spec.HostPort, directaccess.ErrPortInUse))
		}
		return fail(err)
	}
	var out struct {
		NetworkSettings struct {
			Ports map[string][]proxyPortBinding `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if err := h.engine.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", &out); err != nil {
		return fail(err)
	}
	bound := out.NetworkSettings.Ports[port]
	if len(bound) == 0 {
		return fail(errors.New("docker published no host port for the proxy"))
	}
	n, err := strconv.Atoi(bound[0].HostPort)
	if err != nil || n == 0 {
		return fail(fmt.Errorf("docker reported host port %q", bound[0].HostPort))
	}
	return n, nil
}

// portTaken recognises Docker's two phrasings of a bind that failed because
// something else holds the port.
func portTaken(err error) bool {
	m := err.Error()
	return strings.Contains(m, "port is already allocated") || strings.Contains(m, "address already in use")
}

func (h *ProxyHost) ListProxies(ctx context.Context) ([]directaccess.ProxyContainer, error) {
	var rows []struct {
		State  string            `json:"State"`
		Labels map[string]string `json:"Labels"`
	}
	q := url.Values{"all": {"1"}, "filters": {`{"label":["` + directaccess.IDLabel + `"]}`}}
	if err := h.engine.do(ctx, http.MethodGet, "/containers/json?"+q.Encode(), &rows); err != nil {
		return nil, err
	}
	var out []directaccess.ProxyContainer
	for _, r := range rows {
		id, err := strconv.ParseInt(r.Labels[directaccess.IDLabel], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, directaccess.ProxyContainer{AccessID: id, Running: r.State == "running"})
	}
	return out, nil
}

func (h *ProxyHost) RemoveProxy(ctx context.Context, accessID int64) error {
	return h.engine.Remove(ctx, directaccess.ContainerName(accessID), true, false)
}

func (h *ProxyHost) Extend(ctx context.Context, accessID int64, deadline time.Time) error {
	var stderr bytes.Buffer
	code, err := h.exec.Exec(ctx, directaccess.ContainerName(accessID), databases.ExecRequest{
		Cmd:    []string{proxyEntrypoint, "extend", "--state", proxyState, strconv.FormatInt(deadline.Unix(), 10)},
		Stdout: io.Discard, Stderr: &stderr,
	})
	var apiErr *engineError
	if errors.As(err, &apiErr) && (apiErr.status == http.StatusNotFound || apiErr.status == http.StatusConflict) {
		return directaccess.ErrGone
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("relist-proxy extend exited %d: %s", code, strings.TrimSpace(stderr.String()))
	}
	return nil
}
