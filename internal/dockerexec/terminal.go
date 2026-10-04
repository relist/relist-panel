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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"relist-panel/internal/terminal"
)

// TerminalExec is the real terminal.Exec: HTTP over the daemon's unix socket.
type TerminalExec struct {
	socketPath string
	client     *http.Client
}

// NewTerminalExec constructs an adapter over the socket at socketPath.
func NewTerminalExec(socketPath string) *TerminalExec {
	return &TerminalExec{
		socketPath: socketPath,
		client:     newSocketClient(socketPath),
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

	inspectPath := "/exec/" + url.PathEscape(execID) + "/json"
	inspectResp, inspectBody, err := e.send(ctx, http.MethodGet, inspectPath, nil)
	if err != nil {
		return false, err
	}
	if !ok(inspectResp.StatusCode) {
		return false, apiError(inspectPath, inspectResp.Status, inspectBody)
	}
	var inspected struct {
		ExitCode int `json:"ExitCode"`
	}
	if err := json.Unmarshal(inspectBody, &inspected); err != nil {
		return false, fmt.Errorf("docker api %s: decode: %w", inspectPath, err)
	}
	return inspected.ExitCode == 0, nil
}

// Start opens a TTY exec and returns the hijacked stream. The create carries
// the requested shell, user, TERM and initial console size; the start asks the
// daemon to upgrade the connection, and the 101's connection — with its
// reader — becomes the Session.
func (e *TerminalExec) Start(ctx context.Context, containerID string, opts terminal.ExecOptions) (terminal.Session, error) {
	createPath := "/containers/" + url.PathEscape(containerID) + "/exec"
	consoleSize := [2]uint16{opts.Rows, opts.Cols}
	resp, body, err := e.send(ctx, http.MethodPost, createPath, createExecRequest{
		Cmd:          []string{opts.Shell},
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

// ExitCode reads ExitCode from GET /exec/{id}/json.
func (s *session) ExitCode(ctx context.Context) (int, error) {
	path := "/exec/" + url.PathEscape(s.execID) + "/json"
	resp, body, err := s.exec.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, err
	}
	if !ok(resp.StatusCode) {
		return 0, apiError(path, resp.Status, body)
	}
	var inspected struct {
		ExitCode int `json:"ExitCode"`
	}
	if err := json.Unmarshal(body, &inspected); err != nil {
		return 0, fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	return inspected.ExitCode, nil
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
