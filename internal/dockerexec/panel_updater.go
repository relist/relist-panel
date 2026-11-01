package dockerexec

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"relist-panel/internal/panelupdate"
)

// PanelUpdaterHost implements panelupdate.UpdaterHost for the Updater
// process: it shells out to `docker compose`, as the panel does, and uses the
// Engine API for the Container lookups.
type PanelUpdaterHost struct {
	engine *Engine
	// runCompose runs `docker compose` with args; injectable for tests.
	runCompose func(ctx context.Context, args []string) ([]byte, error)
	client     *http.Client
}

func NewPanelUpdaterHost(socketPath string) *PanelUpdaterHost {
	return &PanelUpdaterHost{engine: NewEngine(socketPath), runCompose: execCompose}
}

func execCompose(ctx context.Context, args []string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

// updaterComposeArgs is the common start of the Install's `docker compose` command:
// its project and directory, and its own files in label order. The Install
// directory's .env is read as it always is.
func updaterComposeArgs(p panelupdate.Plan, rest ...string) []string {
	args := []string{"-p", p.Project, "--project-directory", p.InstallDir}
	for _, f := range p.ConfigFiles {
		args = append(args, "-f", f)
	}
	return append(args, rest...)
}

func (h *PanelUpdaterHost) compose(ctx context.Context, p panelupdate.Plan, rest ...string) error {
	out, err := h.runCompose(ctx, updaterComposeArgs(p, rest...))
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (h *PanelUpdaterHost) ComposeUp(ctx context.Context, p panelupdate.Plan) error {
	return h.compose(ctx, p, "up", "-d")
}

func (h *PanelUpdaterHost) ComposeStop(ctx context.Context, p panelupdate.Plan, service string) error {
	return h.compose(ctx, p, "stop", service)
}

// Containers lists the project's Containers of one Service by Compose labels.
// The name of a Service is never used as a DNS name: a user Service on the
// shared network can have the same one.
func (h *PanelUpdaterHost) Containers(ctx context.Context, project, service string) ([]panelupdate.ContainerInfo, error) {
	filters, err := json.Marshal(map[string][]string{"label": {
		"com.docker.compose.project=" + project,
		"com.docker.compose.service=" + service,
		"com.docker.compose.oneoff=False",
	}})
	if err != nil {
		return nil, err
	}
	var listed []struct {
		ID              string `json:"Id"`
		ImageID         string `json:"ImageID"`
		State           string `json:"State"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := h.engine.do(ctx, http.MethodGet, "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), &listed); err != nil {
		return nil, err
	}
	out := make([]panelupdate.ContainerInfo, 0, len(listed))
	for _, c := range listed {
		info := panelupdate.ContainerInfo{ID: c.ID, ImageID: c.ImageID, Running: c.State == "running", IPs: map[string]string{}}
		for name, n := range c.NetworkSettings.Networks {
			info.IPs[name] = n.IPAddress
		}
		out = append(out, info)
	}
	return out, nil
}

// Healthz asks Traefik for the panel's /healthz at its address on the shared
// network, with the domain as Host and SNI. The certificate is not verified:
// Traefik serves its default one until Let's Encrypt has issued.
func (h *PanelUpdaterHost) Healthz(ctx context.Context, ip, domain string) error {
	client := h.client
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{ServerName: domain, InsecureSkipVerify: true}, //nolint:gosec // see above
				DisableKeepAlives: true,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+ip+"/healthz", nil)
	if err != nil {
		return err
	}
	req.Host = domain
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz answered %s", resp.Status)
	}
	return nil
}

var _ panelupdate.UpdaterHost = (*PanelUpdaterHost)(nil)
