package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"relist-panel/internal/panelupdate"
)

// PanelSelf implements panelupdate.SelfInspector: Docker sets a container's
// hostname to its ID, so inspecting the hostname over the Engine API returns
// the panel's own Container.
type PanelSelf struct {
	engine   *Engine
	hostname func() (string, error)
}

func NewPanelSelf(socketPath string) *PanelSelf {
	return &PanelSelf{engine: NewEngine(socketPath), hostname: os.Hostname}
}

func (p *PanelSelf) InspectSelf(ctx context.Context) (panelupdate.Self, error) {
	host, err := p.hostname()
	if err != nil || host == "" {
		return panelupdate.Self{}, errors.New("no hostname to inspect")
	}
	var inspected struct {
		Image  string `json:"Image"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := p.engine.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(host)+"/json", &inspected); err != nil {
		return panelupdate.Self{}, err
	}
	return panelupdate.Self{ImageID: inspected.Image, Labels: inspected.Config.Labels}, nil
}

// PanelHelper implements panelupdate.Helper: one `docker run --rm` of the
// panel's own image per command (ADR-0010), the Install directory mounted
// read-only at its real path.
type PanelHelper struct {
	runDocker func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
}

func NewPanelHelper() *PanelHelper { return &PanelHelper{runDocker: execDocker} }

func (h *PanelHelper) Run(ctx context.Context, run panelupdate.HelperRun, stdout io.Writer) error {
	var stderr bytes.Buffer
	err := h.runDocker(ctx, helperArgv(run), nil, stdout, &stderr)
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(stderr.String())
	var exit exitCoder
	if errors.As(err, &exit) {
		return &panelupdate.HelperExitError{Code: exit.ExitCode(), Stderr: message}
	}
	return fmt.Errorf("docker run: %w: %s", err, message)
}

// helperArgv is the whole command line, argv[0] included. The mounts are the
// Install directory and, for a Preflight, its scratch directory, both
// read-only; DAC_READ_SEARCH lets root read a 0600
// .env owned by someone else, and nothing can write.
func helperArgv(run panelupdate.HelperRun) []string {
	argv := []string{
		"docker", "run", "--rm",
		"--network", "none",
		"--read-only",
		"--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--cap-add", "DAC_READ_SEARCH",
		"--security-opt", "no-new-privileges",
		"--memory", "256m",
		"-v", run.Dir + ":" + run.Dir + ":ro",
	}
	if run.Scratch != "" {
		argv = append(argv, "-v", run.Scratch+":"+run.Scratch+":ro")
	}
	argv = append(argv, "--entrypoint", run.Argv[0], run.Image)
	return append(argv, run.Argv[1:]...)
}

// PanelDocker implements panelupdate.Docker over the Engine API.
type PanelDocker struct {
	engine *Engine
}

func NewPanelDocker(socketPath string) *PanelDocker {
	return &PanelDocker{engine: NewEngine(socketPath)}
}

// Pull pulls ref through the Engine API (`POST /images/create`), as the CLI
// does, and returns Docker's own words when the pull fails. The Engine reports
// a failure inside the 200 stream, so the stream is read to its end.
func (d *PanelDocker) Pull(ctx context.Context, ref string) error {
	repo, tag := ref, ""
	if i := strings.Index(ref, "@"); i >= 0 {
		repo, tag = ref[:i], ref[i+1:]
	}
	query := url.Values{"fromImage": {repo}}
	if tag != "" {
		query.Set("tag", tag)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/images/create?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := d.engine.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return pullMessage(body, resp.Status)
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read the pull stream: %w", err)
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
}

func pullMessage(body []byte, status string) error {
	var msg struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &msg) == nil && msg.Message != "" {
		return errors.New(msg.Message)
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return errors.New(text)
	}
	return errors.New(status)
}

// ConfigHashes lists the config-hash label of every Container of project.
func (d *PanelDocker) ConfigHashes(ctx context.Context, project string) ([]panelupdate.ServiceHash, error) {
	filters, err := json.Marshal(map[string][]string{"label": {
		"com.docker.compose.project=" + project,
		"com.docker.compose.oneoff=False",
	}})
	if err != nil {
		return nil, err
	}
	var listed []struct {
		Labels map[string]string `json:"Labels"`
	}
	path := "/containers/json?all=1&filters=" + url.QueryEscape(string(filters))
	if err := d.engine.do(ctx, http.MethodGet, path, &listed); err != nil {
		return nil, err
	}
	out := make([]panelupdate.ServiceHash, 0, len(listed))
	for _, c := range listed {
		out = append(out, panelupdate.ServiceHash{
			Service: c.Labels["com.docker.compose.service"], Hash: c.Labels["com.docker.compose.config-hash"],
		})
	}
	return out, nil
}

var (
	_ panelupdate.Docker        = (*PanelDocker)(nil)
	_ panelupdate.SelfInspector = (*PanelSelf)(nil)
	_ panelupdate.Helper        = (*PanelHelper)(nil)
)

// UpdaterState inspects the Updater Container by name. A Container that is
// created, restarting or paused counts as running: it may still write.
func (d *PanelDocker) UpdaterState(ctx context.Context, name string) (panelupdate.UpdaterState, error) {
	var inspected struct {
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	err := d.engine.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", &inspected)
	var apiErr *engineError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		return panelupdate.UpdaterState{}, nil
	}
	if err != nil {
		return panelupdate.UpdaterState{}, err
	}
	status := inspected.State.Status
	return panelupdate.UpdaterState{Exists: true, Running: status != "exited" && status != "dead"}, nil
}

// RemoveUpdater removes a stopped Updater Container, without force.
func (d *PanelDocker) RemoveUpdater(ctx context.Context, name string) error {
	return d.engine.Remove(ctx, name, false, false)
}
