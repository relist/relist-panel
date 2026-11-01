package dockerexec

import (
	"bytes"
	"context"
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

// helperArgv is the whole command line, argv[0] included. The one mount is
// the Install directory, read-only; DAC_READ_SEARCH lets root read a 0600
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
		"--entrypoint", run.Argv[0],
		run.Image,
	}
	return append(argv, run.Argv[1:]...)
}

var (
	_ panelupdate.SelfInspector = (*PanelSelf)(nil)
	_ panelupdate.Helper        = (*PanelHelper)(nil)
)
