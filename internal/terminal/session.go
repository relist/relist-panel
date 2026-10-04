// This file owns the terminal's WebSocket session: the query validation that
// happens before the upgrade, the upgrade itself, the resolution of the target
// Container and shell through the Exec seam, and the four goroutines that carry
// a live shell — output to the browser, input from it, the ping loop that
// notices a dropped network, and the resize worker that keeps a slow resize from
// blocking keystrokes.

package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// readLimit is the most a single frame may carry: a large paste fits.
const (
	readLimit        = 64 << 10
	minSize          = 1
	maxSize          = 500
	defaultRows      = 24
	defaultCols      = 80
	exitCodeTimeout  = 5 * time.Second
	controlWriteTime = 5 * time.Second
)

// controlFrame is an incoming JSON text frame. Only resize is defined in that
// direction; the fields a frame does not carry stay zero.
type controlFrame struct {
	Type string `json:"type"`
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

// readyFrame announces a live session with the resolved target.
type readyFrame struct {
	Type      string `json:"type"`
	Shell     string `json:"shell"`
	User      string `json:"user"`
	Container string `json:"container"`
}

// exitFrame reports the shell's exit code once its stream has ended.
type exitFrame struct {
	Type string `json:"type"`
	Code int    `json:"code"`
}

// errorFrame reports a failure that could only be known after the upgrade.
type errorFrame struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// terminal upgrades one request to a WebSocket and runs a shell in a Container
// of the named Service. Validation and Compose resolution answer on the HTTP
// response, before the upgrade; every later failure is an error frame, because
// a browser WebSocket cannot read the body of a failed upgrade.
func (s *Service) terminal(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	service := query.Get("service")
	if service == "" {
		writeError(w, http.StatusBadRequest, "service is required")
		return
	}
	shellChoice, ok := parseShell(query.Get("shell"))
	if !ok {
		writeError(w, http.StatusBadRequest, "shell must be one of auto, bash, sh")
		return
	}
	userChoice, ok := parseUser(query.Get("user"))
	if !ok {
		writeError(w, http.StatusBadRequest, "user must be one of default, root")
		return
	}
	rows, ok := parseSize(query, "rows", defaultRows)
	if !ok {
		writeError(w, http.StatusBadRequest, "rows must be between 1 and 500")
		return
	}
	cols, ok := parseSize(query, "cols", defaultCols)
	if !ok {
		writeError(w, http.StatusBadRequest, "cols must be between 1 and 500")
		return
	}

	req, ok := s.composeRequest(w, r)
	if !ok {
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)

	select {
	case s.sessions <- struct{}{}:
		defer func() { <-s.sessions }()
	default:
		s.fail(conn, fmt.Sprintf("Too many open terminal sessions (%d) — close one first", s.maxSessions))
		return
	}

	containers, err := s.exec.ServiceContainers(r.Context(), req.ProjectName, service)
	if err != nil {
		s.fail(conn, err.Error())
		return
	}
	target, ok := pickContainer(containers, query.Get("container"))
	if !ok {
		s.fail(conn, fmt.Sprintf("Service %q has no running container — deploy or start it first", service))
		return
	}

	shellPath, shellName, err := s.resolveShell(r.Context(), target.ID, shellChoice)
	if err != nil {
		s.fail(conn, err.Error())
		return
	}

	session, err := s.exec.Start(r.Context(), target.ID, ExecOptions{
		Shell: shellPath,
		User:  userFlag(userChoice),
		Rows:  rows,
		Cols:  cols,
	})
	if err != nil {
		s.fail(conn, err.Error())
		return
	}
	defer session.Close()

	if err := writeControl(r.Context(), conn, readyFrame{
		Type:      "ready",
		Shell:     shellName,
		User:      userChoice,
		Container: target.Name,
	}); err != nil {
		return
	}

	s.run(conn, session)
}

// run carries one live session: output to the browser, input from it, a ping
// loop that notices a dropped network, and a resize worker that keeps a slow
// Docker resize call off the input pump. The first pump to end cancels the
// rest; the handler returns once the output pump has stopped, which is when the
// shell's stream has ended.
func (s *Service) run(conn *websocket.Conn, session Session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Closing the Session unblocks the output pump's Read when something
	// other than the shell's own EOF ends the session.
	go func() {
		<-ctx.Done()
		_ = session.Close()
	}()

	resize := make(chan [2]uint16, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case size := <-resize:
				_ = session.Resize(ctx, size[0], size[1])
			}
		}
	}()

	go s.pingLoop(ctx, conn, cancel)

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				writeCtx, writeCancel := context.WithTimeout(ctx, controlWriteTime)
				werr := conn.Write(writeCtx, websocket.MessageBinary, buf[:n])
				writeCancel()
				if werr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if _, err := session.Write(data); err != nil {
					cancel()
					return
				}
			case websocket.MessageText:
				var ctrl controlFrame
				if err := json.Unmarshal(data, &ctrl); err != nil || ctrl.Type != "resize" {
					continue
				}
				pushResize(resize, [2]uint16{clampSize(ctrl.Rows), clampSize(ctrl.Cols)})
			}
		}
	}()

	<-outputDone

	if ctx.Err() == nil {
		exitCtx, exitCancel := context.WithTimeout(context.Background(), exitCodeTimeout)
		code, err := session.ExitCode(exitCtx)
		exitCancel()
		if err == nil {
			_ = writeControl(context.Background(), conn, exitFrame{Type: "exit", Code: code})
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// resolveShell probes the Container through the seam and answers with the shell
// path to run and the short name the browser sees. auto prefers bash and falls
// back to sh; an explicit choice is probed for and refused with the copy the UI
// shows.
func (s *Service) resolveShell(ctx context.Context, containerID, choice string) (path, name string, err error) {
	switch choice {
	case "bash":
		present, err := s.exec.HasShell(ctx, containerID, "/bin/bash")
		if err != nil {
			return "", "", err
		}
		if !present {
			return "", "", errors.New("bash is not installed in this image — use sh")
		}
		return "/bin/bash", "bash", nil
	case "sh":
		present, err := s.exec.HasShell(ctx, containerID, "/bin/sh")
		if err != nil {
			return "", "", err
		}
		if !present {
			return "", "", errors.New("No shell found in this image (/bin/bash, /bin/sh)")
		}
		return "/bin/sh", "sh", nil
	default: // auto
		present, err := s.exec.HasShell(ctx, containerID, "/bin/bash")
		if err != nil {
			return "", "", err
		}
		if present {
			return "/bin/bash", "bash", nil
		}
		present, err = s.exec.HasShell(ctx, containerID, "/bin/sh")
		if err != nil {
			return "", "", err
		}
		if present {
			return "/bin/sh", "sh", nil
		}
		return "", "", errors.New("No shell found in this image (/bin/bash, /bin/sh)")
	}
}

// pingLoop keeps the connection honest: on every interval it pings and waits up
// to the timeout for a pong. The input pump reads the pong; a client that has
// stopped reading stops answering, and the session is cancelled.
func (s *Service) pingLoop(ctx context.Context, conn *websocket.Conn, cancel context.CancelFunc) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, s.pingTimeout)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

// fail writes an error frame and closes the connection. It is used only after
// the upgrade, where a frame is the only channel a browser can read.
func (s *Service) fail(conn *websocket.Conn, message string) {
	_ = writeControl(context.Background(), conn, errorFrame{Type: "error", Message: message})
	_ = conn.Close(websocket.StatusPolicyViolation, "")
}

// writeControl writes one JSON text frame with a bounded context, so a dead
// client cannot pin the handler.
func writeControl(ctx context.Context, conn *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, controlWriteTime)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}

// pushResize offers a size to the single-slot resize channel, replacing a stale
// pending size rather than blocking the input pump. The worker is the only
// receiver, so after at most one replacement the send succeeds.
func pushResize(resize chan [2]uint16, size [2]uint16) {
	for {
		select {
		case resize <- size:
			return
		default:
			select {
			case <-resize:
			default:
			}
		}
	}
}

// pickContainer chooses the target from the Service's running Containers. An id
// that is not in the list counts as none: a Container from another Service,
// another Compose, or the panel itself is never a valid target. With no id the
// lowest-numbered Container is the default.
func pickContainer(containers []Container, id string) (Container, bool) {
	if id != "" {
		for _, container := range containers {
			if container.ID == id {
				return container, true
			}
		}
		return Container{}, false
	}
	if len(containers) == 0 {
		return Container{}, false
	}
	lowest := containers[0]
	for _, container := range containers[1:] {
		if container.Number < lowest.Number {
			lowest = container
		}
	}
	return lowest, true
}

// parseShell validates the shell choice, defaulting to auto.
func parseShell(value string) (string, bool) {
	switch value {
	case "":
		return "auto", true
	case "auto", "bash", "sh":
		return value, true
	default:
		return "", false
	}
}

// parseUser validates the user choice, defaulting to the image's own user.
func parseUser(value string) (string, bool) {
	switch value {
	case "":
		return "default", true
	case "default", "root":
		return value, true
	default:
		return "", false
	}
}

// userFlag maps the wire choice to the exec's user: "" is the image's own user,
// "0" is root.
func userFlag(choice string) string {
	if choice == "root" {
		return "0"
	}
	return ""
}

// parseSize reads one 1–500 size query parameter, defaulting when absent.
func parseSize(query url.Values, key string, fallback uint16) (uint16, bool) {
	if !query.Has(key) {
		return fallback, true
	}
	value, err := strconv.Atoi(query.Get(key))
	if err != nil || value < minSize || value > maxSize {
		return 0, false
	}
	return uint16(value), true
}

// clampSize brings a resize frame's rows or cols into the 1–500 range.
func clampSize(value int) uint16 {
	switch {
	case value < minSize:
		return minSize
	case value > maxSize:
		return maxSize
	default:
		return uint16(value)
	}
}
