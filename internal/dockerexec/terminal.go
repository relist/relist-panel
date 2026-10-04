// This file implements terminal.Exec for real, over the Docker Engine API on
// the mounted socket (ADR-0006's direct API path, the same socket
// ContainerSource uses). The interactive Session is the one exception to the
// package's plain HTTP client: the daemon answers POST /exec/{id}/start with a
// 101 and hands the raw connection to the client, so Start dials the socket
// itself, writes the request and reads the response by hand, then carries that
// same connection — and the bufio.Reader holding any bytes already read past
// the headers — as the bidirectional TTY stream.

package dockerexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/hostmetrics"
	"relist-panel/internal/terminal"
)

// wrapperMarker is the wrapper script's $0 (internal/terminal's wrapperCmd):
// Docker maps an exec's Cmd[0] to ProcessConfig.entrypoint and Cmd[1:] to
// ProcessConfig.arguments, so the marker sits at arguments[2]. The sweep
// matches it to tell a panel shell from any other exec a Container runs.
const wrapperMarker = "relist-terminal"

// TerminalExec is the real terminal.Exec: HTTP over the daemon's unix socket.
type TerminalExec struct {
	socketPath string
	client     *http.Client
	// procRoot is the mounted host proc tree, where an exec's host pid maps
	// to the pid inside its Container's namespace.
	procRoot string
}

// NewTerminalExec constructs an adapter over the socket at socketPath, reading
// container-namespace pids from the host proc tree at procRoot. An empty
// procRoot takes hostmetrics.DefaultProcRoot.
func NewTerminalExec(socketPath, procRoot string) *TerminalExec {
	if procRoot == "" {
		procRoot = hostmetrics.DefaultProcRoot
	}
	return &TerminalExec{
		socketPath: socketPath,
		client:     newSocketClient(socketPath),
		procRoot:   procRoot,
	}
}

var _ terminal.Exec = (*TerminalExec)(nil)

// Available reports whether the daemon answers GET /_ping at all.
func (e *TerminalExec) Available(ctx context.Context) error {
	resp, body, err := e.send(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	if !ok(resp.StatusCode) {
		return apiError("/_ping", resp.Status, body)
	}
	return nil
}

// ServiceContainers reads GET /containers/json filtered on the Compose labels
// for the project and service, and maps each row to the Container the terminal
// addresses it by: the id, the name without Docker's leading slash, and the
// container number its Compose label carries. The rows come back ordered by
// that number.
func (e *TerminalExec) ServiceContainers(ctx context.Context, project, service string) ([]terminal.Container, error) {
	filters, err := json.Marshal(map[string][]string{
		"label": {
			"com.docker.compose.project=" + project,
			"com.docker.compose.service=" + service,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("docker api /containers/json: encode filters: %w", err)
	}
	path := "/containers/json?" + url.Values{"filters": {string(filters)}}.Encode()

	resp, body, err := e.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp.StatusCode) {
		return nil, apiError(path, resp.Status, body)
	}

	var apiContainers []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(body, &apiContainers); err != nil {
		return nil, fmt.Errorf("docker api %s: decode: %w", path, err)
	}

	containers := make([]terminal.Container, 0, len(apiContainers))
	for _, api := range apiContainers {
		name := ""
		if len(api.Names) > 0 {
			name = strings.TrimPrefix(api.Names[0], "/")
		}
		number, _ := strconv.Atoi(api.Labels["com.docker.compose.container-number"])
		containers = append(containers, terminal.Container{ID: api.ID, Name: name, Number: number})
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Number < containers[j].Number })
	return containers, nil
}

// OrphanShells lists the marked terminal shells a crashed panel left running:
// for every running Container of one of the named projects, the execs that are
// Running and carry the wrapper marker. The exec's host pid is read from the
// daemon, then mapped to the last NSpid field of its <procRoot>/<pid>/status —
// the pid inside the Container's own namespace, which is the one a kill there
// addresses. An exec whose proc entry has vanished is skipped: it is already
// gone, which is the outcome the sweep wants. A Container or exec that fails to
// inspect does not stop the others: what was found is returned beside the
// joined errors, so one bad exec cannot leave the rest of the shells running.
func (e *TerminalExec) OrphanShells(ctx context.Context, projects []string) ([]terminal.OrphanShell, error) {
	if len(projects) == 0 {
		return nil, nil
	}
	known := make(map[string]bool, len(projects))
	for _, project := range projects {
		known[project] = true
	}

	containers, err := e.projectContainers(ctx, known)
	if err != nil {
		return nil, err
	}

	var orphans []terminal.OrphanShell
	var errs []error
	for _, containerID := range containers {
		execIDs, err := e.containerExecIDs(ctx, containerID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, execID := range execIDs {
			pid, marked, err := e.markedExecPID(ctx, execID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if marked {
				orphans = append(orphans, terminal.OrphanShell{ContainerID: containerID, PID: pid})
			}
		}
	}
	return orphans, errors.Join(errs...)
}

// projectContainers lists the ids of the running Containers whose Compose
// project label is in known. The project set is matched client-side because
// Docker ANDs repeated label filters, so a single /containers/json request
// cannot express "one of these projects"; the list is small, and the metrics
// collector already reads it the same way.
func (e *TerminalExec) projectContainers(ctx context.Context, known map[string]bool) ([]string, error) {
	path := "/containers/json"
	resp, body, err := e.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !ok(resp.StatusCode) {
		return nil, apiError(path, resp.Status, body)
	}
	var apiContainers []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(body, &apiContainers); err != nil {
		return nil, fmt.Errorf("docker api %s: decode: %w", path, err)
	}

	var ids []string
	for _, api := range apiContainers {
		if known[api.Labels["com.docker.compose.project"]] {
			ids = append(ids, api.ID)
		}
	}
	return ids, nil
}

// containerExecIDs reads a Container's ExecIDs from GET /containers/{id}/json.
// A 404 means the Container vanished between the list and this inspect — a
// redeploy race — so it is skipped, not an error; every other non-2xx stays one.
func (e *TerminalExec) containerExecIDs(ctx context.Context, containerID string) ([]string, error) {
	path := "/containers/" + url.PathEscape(containerID) + "/json"
	resp, body, err := e.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if !ok(resp.StatusCode) {
		return nil, apiError(path, resp.Status, body)
	}
	var inspected struct {
		ExecIDs []string `json:"ExecIDs"`
	}
	if err := json.Unmarshal(body, &inspected); err != nil {
		return nil, fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	return inspected.ExecIDs, nil
}

// markedExecPID reports the container-namespace pid of one exec and whether it
// is a still-running marked shell. A non-marked or exited exec is not an
// orphan, and a marked exec whose proc entry is gone is skipped, not an error.
// A 404 means the exec vanished between the Container's ExecIDs list and this
// inspect — a redeploy race — so it is skipped too; every other non-2xx stays
// an error.
func (e *TerminalExec) markedExecPID(ctx context.Context, execID string) (int, bool, error) {
	path := "/exec/" + url.PathEscape(execID) + "/json"
	resp, body, err := e.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return 0, false, nil
	}
	if !ok(resp.StatusCode) {
		return 0, false, apiError(path, resp.Status, body)
	}
	var inspected struct {
		Running       bool `json:"Running"`
		Pid           int  `json:"Pid"`
		ProcessConfig struct {
			Arguments []string `json:"arguments"`
		} `json:"ProcessConfig"`
	}
	if err := json.Unmarshal(body, &inspected); err != nil {
		return 0, false, fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	if !inspected.Running || len(inspected.ProcessConfig.Arguments) < 3 || inspected.ProcessConfig.Arguments[2] != wrapperMarker {
		return 0, false, nil
	}

	pid, err := e.nspid(inspected.Pid)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return pid, true, nil
}

// nspid reads the container-namespace pid from the last field of the NSpid:
// line in <procRoot>/<pid>/status. The line lists the pid in each namespace
// from the host's inward to the Container's own, so the last field is the pid
// a kill inside the Container addresses.
func (e *TerminalExec) nspid(pid int) (int, error) {
	path := filepath.Join(e.procRoot, strconv.Itoa(pid), "status")
	status, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "NSpid:" {
			continue
		}
		containerPID, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			return 0, fmt.Errorf("proc %s: NSpid %q: %w", path, fields[len(fields)-1], err)
		}
		return containerPID, nil
	}
	return 0, fmt.Errorf("proc %s: no NSpid line", path)
}

// HasShell runs path as a non-TTY exec of `<path> -c "exit 0"` and reads the
// exit code: zero means the shell is present. A daemon that answers "executable
// file not found" — at create on older daemons, at start on newer ones — is the
// same absence, not an error. Anything else non-2xx is surfaced with Docker's
// own words.
func (e *TerminalExec) HasShell(ctx context.Context, containerID, path string) (bool, error) {
	createPath := "/containers/" + url.PathEscape(containerID) + "/exec"
	resp, body, err := e.send(ctx, http.MethodPost, createPath, createExecRequest{
		Cmd: []string{path, "-c", "exit 0"},
		Tty: false,
	})
	if err != nil {
		return false, err
	}
	if !ok(resp.StatusCode) {
		if executableNotFound(resp.StatusCode, body) {
			return false, nil
		}
		return false, apiError(createPath, resp.Status, body)
	}
	execID, err := createdExecID(body)
	if err != nil {
		return false, fmt.Errorf("docker api %s: decode: %w", createPath, err)
	}

	startPath := "/exec/" + url.PathEscape(execID) + "/start"
	startResp, startBody, err := e.send(ctx, http.MethodPost, startPath, startExecRequest{Detach: false, Tty: false})
	if err != nil {
		return false, err
	}
	if !ok(startResp.StatusCode) {
		if executableNotFound(startResp.StatusCode, startBody) {
			return false, nil
		}
		return false, apiError(startPath, startResp.Status, startBody)
	}

	code, err := e.waitExec(ctx, execID)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// Kill signals pid in the Container with sig, through the shell's builtin kill
// rather than a kill executable some minimal images lack: a non-TTY exec of
// `/bin/sh -c "kill -<sig> <pid>"` as root. A non-zero exit means the pid is
// gone, which the liveness probe (sig "0") relies on, so it is ErrNoProcess.
// The adapter knows nothing of the wrapper: pid is the caller's to identify.
func (e *TerminalExec) Kill(ctx context.Context, containerID string, pid int, sig string) error {
	createPath := "/containers/" + url.PathEscape(containerID) + "/exec"
	resp, body, err := e.send(ctx, http.MethodPost, createPath, createExecRequest{
		Cmd:  []string{"/bin/sh", "-c", "kill -" + sig + " " + strconv.Itoa(pid)},
		User: "0",
		Tty:  false,
	})
	if err != nil {
		return err
	}
	if !ok(resp.StatusCode) {
		return apiError(createPath, resp.Status, body)
	}
	execID, err := createdExecID(body)
	if err != nil {
		return fmt.Errorf("docker api %s: decode: %w", createPath, err)
	}

	startPath := "/exec/" + url.PathEscape(execID) + "/start"
	startResp, startBody, err := e.send(ctx, http.MethodPost, startPath, startExecRequest{Detach: false, Tty: false})
	if err != nil {
		return err
	}
	if !ok(startResp.StatusCode) {
		return apiError(startPath, startResp.Status, startBody)
	}

	code, err := e.waitExec(ctx, execID)
	if err != nil {
		return err
	}
	if code != 0 {
		return terminal.ErrNoProcess
	}
	return nil
}

// Start opens a TTY exec and returns the hijacked stream. The create carries
// the requested shell, user, TERM and initial console size; the start asks the
// daemon to upgrade the connection, and the 101's connection — with its
// reader — becomes the Session.
func (e *TerminalExec) Start(ctx context.Context, containerID string, opts terminal.ExecOptions) (terminal.Session, error) {
	createPath := "/containers/" + url.PathEscape(containerID) + "/exec"
	consoleSize := [2]uint16{opts.Rows, opts.Cols}
	resp, body, err := e.send(ctx, http.MethodPost, createPath, createExecRequest{
		Cmd:          opts.Cmd,
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		User:         opts.User,
		Env:          []string{"TERM=xterm-256color"},
		ConsoleSize:  &consoleSize,
	})
	if err != nil {
		return nil, err
	}
	if !ok(resp.StatusCode) {
		return nil, apiError(createPath, resp.Status, body)
	}
	execID, err := createdExecID(body)
	if err != nil {
		return nil, fmt.Errorf("docker api %s: decode: %w", createPath, err)
	}

	startPath := "/exec/" + url.PathEscape(execID) + "/start"
	conn, reader, err := e.hijack(ctx, startPath, startExecRequest{Detach: false, Tty: true})
	if err != nil {
		return nil, err
	}
	return &session{exec: e, conn: conn, reader: reader, execID: execID}, nil
}

// hijack dials the socket, writes the start request with the upgrade headers
// and reads the 101 by hand. It returns the raw connection and the buffered
// reader holding any bytes the daemon already sent past the response headers —
// those bytes are the stream's, so the Session must read through the same
// reader.
func (e *TerminalExec) hijack(ctx context.Context, path string, in any) (net.Conn, *bufio.Reader, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, nil, fmt.Errorf("docker api %s: encode: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", e.socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, nil, apiError(path, resp.Status, payload)
	}
	return conn, reader, nil
}

// session is one live interactive exec: the hijacked connection both ways, and
// the resize and exit-code calls the daemon offers out of band.
type session struct {
	exec   *TerminalExec
	conn   net.Conn
	reader *bufio.Reader
	execID string
}

var _ terminal.Session = (*session)(nil)

func (s *session) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *session) Write(p []byte) (int, error) {
	return s.conn.Write(p)
}

func (s *session) Close() error {
	return s.conn.Close()
}

// Resize sends POST /exec/{id}/resize?h=<rows>&w=<cols>.
func (s *session) Resize(ctx context.Context, rows, cols uint16) error {
	path := "/exec/" + url.PathEscape(s.execID) + "/resize?" + url.Values{
		"h": {strconv.Itoa(int(rows))},
		"w": {strconv.Itoa(int(cols))},
	}.Encode()
	resp, body, err := s.exec.send(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	if !ok(resp.StatusCode) {
		return apiError(path, resp.Status, body)
	}
	return nil
}

// ExitCode waits for GET /exec/{id}/json to report the finished exec's code.
func (s *session) ExitCode(ctx context.Context) (int, error) {
	return s.exec.waitExec(ctx, s.execID)
}

// waitExec polls GET /exec/{id}/json every 50 ms until the exec is no longer
// Running and its ExitCode is set, then returns the code. The daemon can close
// a stream — and a non-TTY exec can answer its start — a moment before it has
// recorded the exit, and reading then would give a false 0. A ctx that ends
// first is an error, never a guessed code.
func (e *TerminalExec) waitExec(ctx context.Context, execID string) (int, error) {
	path := "/exec/" + url.PathEscape(execID) + "/json"
	for {
		resp, body, err := e.send(ctx, http.MethodGet, path, nil)
		if err != nil {
			if ctx.Err() != nil {
				return 0, fmt.Errorf("docker api %s: exit code not reported: %w", path, ctx.Err())
			}
			return 0, err
		}
		if !ok(resp.StatusCode) {
			return 0, apiError(path, resp.Status, body)
		}
		var inspected struct {
			Running  bool `json:"Running"`
			ExitCode *int `json:"ExitCode"`
		}
		if err := json.Unmarshal(body, &inspected); err != nil {
			return 0, fmt.Errorf("docker api %s: decode: %w", path, err)
		}
		if !inspected.Running && inspected.ExitCode != nil {
			return *inspected.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("docker api %s: exit code not reported: %w", path, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// createExecRequest is POST /containers/{id}/exec's body. User, Env and
// ConsoleSize are omitted when the caller leaves them at their zero value:
// User omitted is the image's own user, and ConsoleSize omitted lets the
// daemon keep the default size.
type createExecRequest struct {
	Cmd          []string   `json:"Cmd"`
	Tty          bool       `json:"Tty"`
	AttachStdin  bool       `json:"AttachStdin,omitempty"`
	AttachStdout bool       `json:"AttachStdout,omitempty"`
	AttachStderr bool       `json:"AttachStderr,omitempty"`
	User         string     `json:"User,omitempty"`
	Env          []string   `json:"Env,omitempty"`
	ConsoleSize  *[2]uint16 `json:"ConsoleSize,omitempty"`
}

// startExecRequest is POST /exec/{id}/start's body. The hijacking body is
// exactly {"Detach":false,"Tty":true}.
type startExecRequest struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// send marshals in (when non-nil) and runs one request through the socket
// client, returning the response and its buffered body. Non-2xx handling is
// left to the caller because the right answer differs per endpoint.
func (e *TerminalExec) send(ctx context.Context, method, path string, in any) (*http.Response, []byte, error) {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return nil, nil, fmt.Errorf("docker api %s: encode: %w", path, err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return nil, nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("docker api %s: read: %w", path, err)
	}
	return resp, payload, nil
}

// createdExecID reads the "Id" of a create-exec answer.
func createdExecID(body []byte) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// ok reports whether status is a success the adapter accepts.
func ok(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

// executableNotFound reports the daemon's "there is no such executable" answer,
// which means the probed shell is absent rather than that the request failed.
// The daemon says it at create on some versions and at start on others (with a
// 500), so the match is on Docker's own words, not on a single status code.
func executableNotFound(status int, body []byte) bool {
	return !ok(status) && strings.Contains(string(body), "executable file not found")
}

// apiError formats a non-2xx answer exactly as ContainerSource.get does, so
// Docker's own words reach the operator (GUIDELINES §8).
func apiError(path, status string, body []byte) error {
	return fmt.Errorf("docker api %s: %s: %s", path, status, strings.TrimSpace(string(body)))
}
