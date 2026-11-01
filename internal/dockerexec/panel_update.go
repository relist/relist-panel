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

// PanelUpdateRunner implements panelupdate.UpdateRunner: the `update` Container
// is one `docker run` of the current panel image, outside the Install and with
// no Compose labels, so `up` never treats it as an orphan of the project.
type PanelUpdateRunner struct {
	runDocker func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
}

func NewPanelUpdateRunner() *PanelUpdateRunner { return &PanelUpdateRunner{runDocker: execDocker} }

// updateArgv is the whole command line, argv[0] included:
//
//	docker run [--rm | -d --name <name>] --network relist-network //	  -v /var/run/docker.sock:/var/run/docker.sock //	  -v <install dir>:<install dir> -v <data dir>:<data dir> //	  -e RELIST_DATA_DIR=<data dir> <image> update <args…>
//
// A detached run has no --rm and no restart policy: the watcher removes the
// Container once the run is settled, and its logs outlive a failure. It is not
// locked down: it needs the socket and both directories.
func updateArgv(spec panelupdate.UpdateSpec, detached bool) []string {
	argv := []string{"docker", "run"}
	if detached {
		argv = append(argv, "-d", "--name", spec.Name)
	} else {
		argv = append(argv, "--rm")
	}
	argv = append(argv,
		"--network", spec.Network,
		"-v", DefaultSocketPath+":"+DefaultSocketPath,
		"-v", spec.InstallDir+":"+spec.InstallDir,
		"-v", spec.DataDir+":"+spec.DataDir,
		"-e", "RELIST_DATA_DIR="+spec.DataDir,
	)
	for _, e := range spec.Env {
		argv = append(argv, "-e", e)
	}
	argv = append(argv, spec.Image, "update")
	return append(argv, spec.Args...)
}

// Preflight runs the Container with --rm and waits for it. A non-zero exit is
// not an error here: the caller reads the exit code (1 and 3 are reports).
func (r *PanelUpdateRunner) Preflight(ctx context.Context, spec panelupdate.UpdateSpec) ([]byte, int, error) {
	var stdout, stderr bytes.Buffer
	err := r.runDocker(ctx, updateArgv(spec, false), nil, &stdout, &stderr)
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var exit exitCoder
	if errors.As(err, &exit) {
		return stdout.Bytes(), exit.ExitCode(), nil
	}
	return nil, 0, fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(stderr.String()))
}

// StartUpdater starts the detached Container; `docker run -d` returns once it
// runs.
func (r *PanelUpdateRunner) StartUpdater(ctx context.Context, spec panelupdate.UpdateSpec) error {
	var stderr bytes.Buffer
	if err := r.runDocker(ctx, updateArgv(spec, true), nil, io.Discard, &stderr); err != nil {
		return fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// StopUpdater is `docker stop <name>`: the Updater gets SIGTERM, which aborts a
// run that is still quiescing.
func (r *PanelUpdateRunner) StopUpdater(ctx context.Context, name string) error {
	var stderr bytes.Buffer
	if err := r.runDocker(ctx, []string{"docker", "stop", name}, nil, io.Discard, &stderr); err != nil {
		return fmt.Errorf("docker stop: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
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
	_ panelupdate.UpdateRunner  = (*PanelUpdateRunner)(nil)
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

// ImageID is the ID of an image the daemon already holds. A digest reference
// is a registry manifest digest, which a Container's image never equals.
func (d *PanelDocker) ImageID(ctx context.Context, ref string) (string, error) {
	var inspected struct {
		ID string `json:"Id"`
	}
	if err := d.engine.do(ctx, http.MethodGet, "/images/"+ref+"/json", &inspected); err != nil {
		return "", err
	}
	if inspected.ID == "" {
		return "", errors.New("the daemon returned no image ID")
	}
	return inspected.ID, nil
}

// ListUpdaters lists the Containers named relist-panel-updater-*.
func (d *PanelDocker) ListUpdaters(ctx context.Context) ([]panelupdate.UpdaterListing, error) {
	filters, err := json.Marshal(map[string][]string{"name": {updaterNamePrefix}})
	if err != nil {
		return nil, err
	}
	var listed []struct {
		Names []string `json:"Names"`
		State string   `json:"State"`
	}
	if err := d.engine.do(ctx, http.MethodGet, "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), &listed); err != nil {
		return nil, err
	}
	var out []panelupdate.UpdaterListing
	for _, c := range listed {
		for _, n := range c.Names {
			// The filter matches anywhere in the name; the prefix is exact.
			if name := strings.TrimPrefix(n, "/"); strings.HasPrefix(name, updaterNamePrefix) {
				out = append(out, panelupdate.UpdaterListing{Name: name, Running: c.State != "exited" && c.State != "dead"})
			}
		}
	}
	return out, nil
}

const updaterNamePrefix = "relist-panel-updater-"
