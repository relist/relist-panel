package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"

	"relist-panel/internal/composes"
	"relist-panel/internal/terminal"
	terminalfake "relist-panel/internal/terminal/faketest"
)

// terminalServicesPath is the listing route under one Compose.
func terminalServicesPath(projectID, environmentID, composeID int64) string {
	return composePath(projectID, environmentID, composeID) + "/terminal/services"
}

type terminalServicesResponse struct {
	Available bool                      `json:"available"`
	Reason    string                    `json:"reason"`
	Services  []terminalServiceResponse `json:"services"`
}

type terminalServiceResponse struct {
	Name       string                      `json:"name"`
	Containers []terminalContainerResponse `json:"containers"`
}

type terminalContainerResponse struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Number int                    `json:"number"`
	Shells terminalShellsResponse `json:"shells"`
}

type terminalShellsResponse struct {
	Bash bool `json:"bash"`
	Sh   bool `json:"sh"`
}

// The listing reports every Service the Compose's status names, with its
// running Containers and which shells each one carries; a Service with no
// running Container is still listed, with an empty container list.
func TestTerminalServicesListsRunningContainersAndShells(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")
	projectName := "relist-c" + strconv.FormatInt(compose.ID, 10)

	server.runner.statuses[projectName] = composes.ComposeStatus{Services: []string{"app", "worker"}}
	server.terminal.Containers = map[string][]terminal.Container{
		projectName + "/app": {{ID: "c1", Name: "relist-c3-app-1", Number: 1}},
	}
	server.terminal.Shells = map[string]bool{
		"c1:/bin/bash": true,
		"c1:/bin/sh":   true,
	}

	response := server.request(t, http.MethodGet, terminalServicesPath(project.ID, env.ID, compose.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var payload terminalServicesResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode terminal services: %v", err)
	}
	if !payload.Available || payload.Reason != "" {
		t.Fatalf("availability = %+v, want available with no reason", payload)
	}
	if len(payload.Services) != 2 {
		t.Fatalf("services = %+v, want app and worker", payload.Services)
	}

	app := payload.Services[0]
	if app.Name != "app" || len(app.Containers) != 1 {
		t.Fatalf("first service = %+v, want app with one container", app)
	}
	container := app.Containers[0]
	if container.ID != "c1" || container.Name != "relist-c3-app-1" || container.Number != 1 {
		t.Fatalf("container = %+v, want c1/relist-c3-app-1/1", container)
	}
	if !container.Shells.Bash || !container.Shells.Sh {
		t.Fatalf("shells = %+v, want bash and sh", container.Shells)
	}

	worker := payload.Services[1]
	if worker.Name != "worker" {
		t.Fatalf("second service = %+v, want worker", worker)
	}
	if worker.Containers == nil || len(worker.Containers) != 0 {
		t.Fatalf("worker containers = %#v, want an empty list, not null", worker.Containers)
	}
}

// When the Engine API cannot be reached the listing is still a 200, but says
// the feature is unavailable and why — as the Volumes listing does.
func TestTerminalServicesReportsTheFeatureUnavailable(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")

	server.terminal.AvailableErr = errors.New("docker socket not reachable")

	response := server.request(t, http.MethodGet, terminalServicesPath(project.ID, env.ID, compose.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var payload terminalServicesResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode terminal services: %v", err)
	}
	if payload.Available {
		t.Fatalf("available = true, want false: %+v", payload)
	}
	if payload.Reason != "docker socket not reachable" {
		t.Fatalf("reason = %q, want the availability error", payload.Reason)
	}
	if payload.Services == nil || len(payload.Services) != 0 {
		t.Fatalf("services = %#v, want an empty list, not null", payload.Services)
	}
}

// An unknown Compose — or an unknown Environment or Project above it — is the
// same 404 the Compose routes answer, before any Engine API call.
func TestTerminalServicesRequiresAKnownComposeAndParents(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")

	for _, tt := range []struct {
		name string
		path string
	}{
		{"unknown compose", terminalServicesPath(project.ID, env.ID, compose.ID+1000)},
		{"unknown environment", terminalServicesPath(project.ID, env.ID+1000, compose.ID)},
		{"unknown project", terminalServicesPath(project.ID+1000, env.ID, compose.ID)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, tt.path, nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
			}
		})
	}
}

// The listing is behind the session cookie like every other API route.
func TestTerminalServicesRequiresAuthentication(t *testing.T) {
	server := newTestServer(t)
	response := server.request(t, http.MethodGet, terminalServicesPath(1, 1, 1), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

// --- The WebSocket session -------------------------------------------------

// terminalControl is every JSON control frame the session can carry, in either
// direction. A frame sets only the fields it means.
type terminalControl struct {
	Type      string `json:"type"`
	Shell     string `json:"shell"`
	User      string `json:"user"`
	Container string `json:"container"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
}

// terminalSessionPath is the WebSocket route under one Compose, with its query.
func terminalSessionPath(projectID, environmentID, composeID int64, query string) string {
	path := composePath(projectID, environmentID, composeID) + "/terminal"
	if query != "" {
		path += "?" + query
	}
	return path
}

// dialTerminal starts a server over the test handler, copies the session cookie
// into header, and dials the WebSocket route. query is the route and its query
// string, for example "/api/…/terminal?service=app".
func dialTerminal(t *testing.T, server *testServer, query string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	httpServer := httptest.NewServer(server.handler)
	t.Cleanup(httpServer.Close)
	if header == nil {
		header = http.Header{}
	}
	for _, cookie := range server.cookies {
		header.Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	return websocket.Dial(context.Background(), httpServer.URL+query, &websocket.DialOptions{HTTPHeader: header})
}

// newTerminalSessionServer is the fixture the session tests share: a logged-in
// server with one Project, its production Environment and one Compose. The last
// return is the Compose's deploy ProjectName.
func newTerminalSessionServer(t *testing.T) (*testServer, int64, int64, int64, string) {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")
	return server, project.ID, env.ID, compose.ID, "relist-c" + strconv.FormatInt(compose.ID, 10)
}

// scriptAppContainer scripts one running Container for the app Service, with
// exactly the named shells present.
func scriptAppContainer(server *testServer, projectName string, container terminal.Container, shells ...string) {
	server.terminal.Containers = map[string][]terminal.Container{
		projectName + "/app": {container},
	}
	server.terminal.Shells = map[string]bool{}
	for _, shell := range shells {
		server.terminal.Shells[container.ID+":"+shell] = true
	}
}

func readMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	return typ, data
}

func readControl(t *testing.T, conn *websocket.Conn, timeout time.Duration) terminalControl {
	t.Helper()
	typ, data := readMessage(t, conn, timeout)
	if typ != websocket.MessageText {
		t.Fatalf("message type = %v, want text (%q)", typ, data)
	}
	var ctrl terminalControl
	if err := json.Unmarshal(data, &ctrl); err != nil {
		t.Fatalf("decode control frame %q: %v", data, err)
	}
	return ctrl
}

func awaitReady(t *testing.T, conn *websocket.Conn) terminalControl {
	t.Helper()
	ctrl := readControl(t, conn, 2*time.Second)
	if ctrl.Type != "ready" {
		t.Fatalf("control frame = %+v, want ready", ctrl)
	}
	return ctrl
}

// wrapperPID is the pid the fake's marked wrapper announces. Every session test
// that awaits ready must first feed this line to the session's Remote: the
// handler reads it before it announces ready, and never forwards it.
const wrapperPID = 4242

// awaitStarted waits until the handler has started at least before+1 sessions
// and returns the (before+1)th, so a test can act as the container side before
// the handler sends ready.
func awaitStarted(t *testing.T, server *testServer, before int) *terminalfake.Session {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		starts := server.terminal.StartsSnapshot()
		if len(starts) > before {
			return starts[before].Session
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// writePID feeds the marked wrapper's pid line to the session, as the container
// side would.
func writePID(t *testing.T, session *terminalfake.Session) {
	t.Helper()
	if _, err := session.Remote.Write([]byte(strconv.Itoa(wrapperPID) + "\r\n")); err != nil {
		t.Fatalf("write the wrapper's pid line: %v", err)
	}
}

// openAndReady dials a session, feeds the wrapper a pid line if the handler got
// as far as starting one, and returns the first control frame. A cap refusal
// arrives with no Start and is returned as its error frame.
func openAndReady(t *testing.T, server *testServer, path string) (*websocket.Conn, terminalControl) {
	t.Helper()
	before := len(server.terminal.StartsSnapshot())
	conn, resp, err := dialTerminal(t, server, path, nil)
	if err != nil {
		t.Fatalf("dial terminal: %v (HTTP %v)", err, resp)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for len(server.terminal.StartsSnapshot()) == before && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if starts := server.terminal.StartsSnapshot(); len(starts) > before {
		writePID(t, starts[before].Session)
	}
	return conn, readControl(t, conn, 2*time.Second)
}

func dialReady(t *testing.T, server *testServer, path string) (*websocket.Conn, terminalControl) {
	t.Helper()
	conn, ctrl := openAndReady(t, server, path)
	t.Cleanup(func() { conn.CloseNow() })
	if ctrl.Type != "ready" {
		t.Fatalf("control frame = %+v, want ready", ctrl)
	}
	return conn, ctrl
}

func writeText(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode control frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write control frame: %v", err)
	}
}

func writeBinary(t *testing.T, conn *websocket.Conn, payload []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write binary frame: %v", err)
	}
}

// awaitClose reads until the server's close frame and asserts the status the
// spec gives an exited shell: 1000.
func awaitClose(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected the connection to close")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusNormalClosure {
		t.Fatalf("close status = %d, want %d", status, websocket.StatusNormalClosure)
	}
}

// A binary keystroke reaches the container side, and container output arrives
// back as a binary frame.
func TestTerminalSessionPumpsBytesBothWays(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	before := len(server.terminal.StartsSnapshot())
	conn, resp, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"), nil)
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	defer conn.CloseNow()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	session := awaitStarted(t, server, before)
	writePID(t, session)
	awaitReady(t, conn)

	writeBinary(t, conn, []byte("ls\r"))
	keystrokes := make([]byte, 3)
	if _, err := io.ReadFull(session.Remote, keystrokes); err != nil {
		t.Fatalf("read keystrokes on Remote: %v", err)
	}
	if string(keystrokes) != "ls\r" {
		t.Fatalf("Remote read %q, want %q", keystrokes, "ls\r")
	}

	if _, err := session.Remote.Write([]byte("out")); err != nil {
		t.Fatalf("write output on Remote: %v", err)
	}
	typ, data := readMessage(t, conn, 2*time.Second)
	if typ != websocket.MessageBinary || string(data) != "out" {
		t.Fatalf("message = %v %q, want binary %q", typ, data, "out")
	}
}

// The wrapper's pid line is the session's own business: the browser receives
// only the shell's output that follows it.
func TestTerminalSessionHidesTheWrapperPID(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	before := len(server.terminal.StartsSnapshot())
	conn, resp, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"), nil)
	if err != nil {
		t.Fatalf("dial terminal: %v (HTTP %v)", err, resp)
	}
	defer conn.CloseNow()
	session := awaitStarted(t, server, before)
	if _, err := session.Remote.Write([]byte("4242\r\n$ ")); err != nil {
		t.Fatalf("write the wrapper's pid line and output: %v", err)
	}
	awaitReady(t, conn)

	typ, data := readMessage(t, conn, 2*time.Second)
	if typ != websocket.MessageBinary || string(data) != "$ " {
		t.Fatalf("message = %v %q, want binary %q", typ, data, "$ ")
	}
}

// The session announces the shell with a marked wrapper: /bin/sh prints its own
// pid and execs the chosen shell, so the session has a pid to hang up and Task
// 7's sweep has a marker. The session builds the argv; the adapter sends it.
func TestTerminalSessionStartsTheMarkedWrapper(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))

	starts := server.terminal.StartsSnapshot()
	if len(starts) != 1 {
		t.Fatalf("Starts = %d, want 1", len(starts))
	}
	want := []string{"/bin/sh", "-c", `echo $$; exec "$1"`, "relist-terminal", "/bin/bash"}
	if got := starts[0].Opts.Cmd; !reflect.DeepEqual(got, want) {
		t.Fatalf("start command = %v, want %v", got, want)
	}
}

// When a session ends the shell it started is hung up: a HUP, then a probe for
// the pid, and a KILL only when the probe still finds it. Escalation is skipped
// only once the pid is reported gone.
func TestTerminalSessionHangsUpTheShellOnDisconnect(t *testing.T) {
	t.Run("escalates to KILL when the probe still sees the pid", func(t *testing.T) {
		server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
		scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

		conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
		conn.CloseNow()

		want := []terminalfake.KillCall{
			{ContainerID: "c1", PID: wrapperPID, Signal: "HUP"},
			{ContainerID: "c1", PID: wrapperPID, Signal: "KILL"},
		}
		if got := waitForNonProbeKills(t, server, want); !reflect.DeepEqual(got, want) {
			t.Fatalf("kills = %+v, want %+v", got, want)
		}
	})

	t.Run("stops after HUP when the probe reports no process", func(t *testing.T) {
		server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
		scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")
		server.terminal.KillExits = map[int]bool{wrapperPID: true}

		conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
		conn.CloseNow()

		want := []terminalfake.KillCall{{ContainerID: "c1", PID: wrapperPID, Signal: "HUP"}}
		if got := waitForNonProbeKills(t, server, want); !reflect.DeepEqual(got, want) {
			t.Fatalf("kills = %+v, want %+v", got, want)
		}
	})
}

// waitForNonProbeKills polls until the signals other than the liveness probe
// (signal "0") match want, and returns them.
func waitForNonProbeKills(t *testing.T, server *testServer, want []terminalfake.KillCall) []terminalfake.KillCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := make([]terminalfake.KillCall, 0, len(want))
		for _, kill := range server.terminal.KillsSnapshot() {
			if kill.Signal != "0" {
				got = append(got, kill)
			}
		}
		if reflect.DeepEqual(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("kills = %+v, want %+v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ready names the shell, user and Container the session resolved, and the exec
// was started with the matching options and the browser's initial size.
func TestTerminalSessionReadyNamesTheResolvedTarget(t *testing.T) {
	t.Run("bash present", func(t *testing.T) {
		server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
		scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash", "/bin/sh")

		_, ready := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app&shell=auto"))
		if ready.Shell != "bash" || ready.User != "default" || ready.Container != "relist-c3-app-1" {
			t.Fatalf("ready = %+v, want bash/default/relist-c3-app-1", ready)
		}
		want := terminal.ExecOptions{
			Shell: "/bin/bash",
			Rows:  24,
			Cols:  80,
			Cmd:   []string{"/bin/sh", "-c", `echo $$; exec "$1"`, "relist-terminal", "/bin/bash"},
		}
		if got := server.terminal.StartsSnapshot()[0].Opts; !reflect.DeepEqual(got, want) {
			t.Fatalf("exec options = %+v, want %+v", got, want)
		}
	})

	t.Run("bash absent", func(t *testing.T) {
		server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
		scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/sh")

		_, ready := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app&shell=auto"))
		if ready.Shell != "sh" {
			t.Fatalf("ready.shell = %q, want sh", ready.Shell)
		}
		want := terminal.ExecOptions{
			Shell: "/bin/sh",
			Rows:  24,
			Cols:  80,
			Cmd:   []string{"/bin/sh", "-c", `echo $$; exec "$1"`, "relist-terminal", "/bin/sh"},
		}
		if got := server.terminal.StartsSnapshot()[0].Opts; !reflect.DeepEqual(got, want) {
			t.Fatalf("exec options = %+v, want %+v", got, want)
		}
	})
}

// user=root starts the exec as uid 0 and says so in ready.
func TestTerminalSessionRunsAsRootOnRequest(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	_, ready := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app&user=root"))
	if ready.User != "root" {
		t.Fatalf("ready.user = %q, want root", ready.User)
	}
	if got := server.terminal.StartsSnapshot()[0].Opts.User; got != "0" {
		t.Fatalf("exec user = %q, want %q", got, "0")
	}
}

// Resize frames reach the session in order, clamped to 1–500.
func TestTerminalSessionForwardsClampedResizes(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
	session := server.terminal.StartsSnapshot()[0].Session

	writeText(t, conn, map[string]any{"type": "resize", "rows": 40, "cols": 120})
	waitForResizes(t, session, [][2]uint16{{40, 120}})
	writeText(t, conn, map[string]any{"type": "resize", "rows": 9000, "cols": 0})
	waitForResizes(t, session, [][2]uint16{{40, 120}, {500, 1}})
}

// waitForResizes polls the session's recorded resizes until they match want.
func waitForResizes(t *testing.T, session interface{ ResizesSnapshot() [][2]uint16 }, want [][2]uint16) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := session.ResizesSnapshot()
		if reflect.DeepEqual(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("resizes = %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An explicit shell that is absent, and auto with neither shell, each get the
// matching error frame and never start an exec.
func TestTerminalSessionReportsAMissingShell(t *testing.T) {
	cases := []struct {
		name    string
		shells  []string
		query   string
		message string
	}{
		{"bash chosen without bash", []string{"/bin/sh"}, "service=app&shell=bash", "bash is not installed in this image — use sh"},
		{"auto with neither shell", nil, "service=app&shell=auto", "No shell found in this image (/bin/bash, /bin/sh)"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
			scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, tt.shells...)

			conn, _, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, tt.query), nil)
			if err != nil {
				t.Fatalf("dial terminal: %v", err)
			}
			defer conn.CloseNow()
			ctrl := readControl(t, conn, 2*time.Second)
			if ctrl.Type != "error" || ctrl.Message != tt.message {
				t.Fatalf("control frame = %+v, want error %q", ctrl, tt.message)
			}
			if starts := server.terminal.StartsSnapshot(); len(starts) != 0 {
				t.Fatalf("Starts = %+v, want none", starts)
			}
		})
	}
}

// A Container that is not in the resolved Service's running list — another
// Service's, another Compose's, or the panel's own body — is refused as no
// running Container, with no shell probe and no exec on the foreign id.
func TestTerminalSessionRefusesAContainerOutsideTheService(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	server.terminal.Containers = map[string][]terminal.Container{
		projectName + "/app":    {{ID: "c1", Name: "relist-c3-app-1", Number: 1}},
		projectName + "/worker": {{ID: "w1", Name: "relist-c3-worker-1", Number: 1}},
		"relist-c999/app":       {{ID: "x1", Name: "relist-c999-app-1", Number: 1}},
	}
	server.terminal.Shells = map[string]bool{
		"c1:/bin/bash": true,
		"w1:/bin/bash": true,
		"x1:/bin/bash": true,
	}

	for _, foreign := range []string{"w1", "x1", "deadbeef"} {
		conn, _, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app&container="+foreign), nil)
		if err != nil {
			t.Fatalf("dial terminal for %q: %v", foreign, err)
		}
		ctrl := readControl(t, conn, 2*time.Second)
		conn.CloseNow()
		want := `Service "app" has no running container — deploy or start it first`
		if ctrl.Type != "error" || ctrl.Message != want {
			t.Fatalf("container %q control frame = %+v, want error %q", foreign, ctrl, want)
		}
	}

	if starts := server.terminal.StartsSnapshot(); len(starts) != 0 {
		t.Fatalf("Starts = %+v, want none", starts)
	}
	for _, call := range server.terminal.ShellCallsSnapshot() {
		if call.ContainerID != "c1" {
			t.Fatalf("HasShell probe = %+v, want none on a foreign Container", call)
		}
	}
}

// With no container id the lowest-numbered running Container is the target,
// whatever order the listing returns.
func TestTerminalSessionDefaultsToTheLowestNumberedContainer(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	server.terminal.Containers = map[string][]terminal.Container{
		projectName + "/app": {
			{ID: "c2", Name: "relist-c3-app-2", Number: 2},
			{ID: "c1", Name: "relist-c3-app-1", Number: 1},
		},
	}
	server.terminal.Shells = map[string]bool{"c1:/bin/bash": true, "c2:/bin/bash": true}

	_, ready := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
	if ready.Container != "relist-c3-app-1" {
		t.Fatalf("ready.container = %q, want relist-c3-app-1", ready.Container)
	}
	if got := server.terminal.StartsSnapshot()[0].ContainerID; got != "c1" {
		t.Fatalf("started ContainerID = %q, want c1", got)
	}
}

// When the shell's stream ends the server reports its exit code and closes 1000.
func TestTerminalSessionReportsTheExitCode(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")
	server.terminal.ExitCode = 3

	conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
	session := server.terminal.StartsSnapshot()[0].Session
	if err := session.Remote.Close(); err != nil {
		t.Fatalf("close Remote: %v", err)
	}
	ctrl := readControl(t, conn, 2*time.Second)
	if ctrl.Type != "exit" || ctrl.Code != 3 {
		t.Fatalf("control frame = %+v, want exit code 3", ctrl)
	}
	awaitClose(t, conn)
}

// A stream that ends before any output still reaches the browser as an exit and
// releases its cap slot, so an eleventh session opens.
func TestTerminalSessionEndsWhenTheStreamEndsBeforeOutput(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	conns := make([]*websocket.Conn, 0, 10)
	for i := 0; i < 10; i++ {
		conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
		conns = append(conns, conn)
	}
	starts := server.terminal.StartsSnapshot()
	if len(starts) != 10 {
		t.Fatalf("Starts = %d, want 10", len(starts))
	}

	first := starts[0].Session
	if err := first.Remote.Close(); err != nil {
		t.Fatalf("close Remote: %v", err)
	}
	ctrl := readControl(t, conns[0], 2*time.Second)
	if ctrl.Type != "exit" {
		t.Fatalf("control frame = %+v, want exit", ctrl)
	}
	awaitClose(t, conns[0])
	select {
	case <-first.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("session was not closed after its stream ended")
	}

	// The slot is freed once the handler returns; dial until the eleventh opens.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, ctrl := openAndReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
		conn.CloseNow()
		if ctrl.Type == "ready" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the eleventh session never opened after a slot was released")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The server-wide cap refuses the eleventh concurrent session with the cap
// error, and closing one frees a slot for a new session.
func TestTerminalSessionEnforcesTheSessionCap(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	conns := make([]*websocket.Conn, 0, 10)
	for i := 0; i < 10; i++ {
		conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
		conns = append(conns, conn)
	}

	over, _, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"), nil)
	if err != nil {
		t.Fatalf("eleventh dial: %v", err)
	}
	ctrl := readControl(t, over, 2*time.Second)
	over.CloseNow()
	want := "Too many open terminal sessions (10) — close one first"
	if ctrl.Type != "error" || ctrl.Message != want {
		t.Fatalf("control frame = %+v, want error %q", ctrl, want)
	}

	starts := server.terminal.StartsSnapshot()
	_ = conns[0].Close(websocket.StatusNormalClosure, "")
	select {
	case <-starts[0].Session.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("the closed session never signalled")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, ctrl := openAndReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
		conn.CloseNow()
		if ctrl.Type == "ready" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a new session never opened after one was closed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A client that stops answering pings is noticed and its Session closed.
func TestTerminalSessionEndsWhenPongsStop(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
	session := server.terminal.StartsSnapshot()[0].Session
	_ = conn // The test stops reading here, so no pong is ever read or sent.

	select {
	case <-session.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not close after pongs stopped")
	}
}

// A resize that is stuck inside the session does not stop keystrokes from
// reaching the container side.
func TestTerminalSessionKeystrokesFlowDuringASlowResize(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	server.terminal.BeforeResize = func() {
		select {
		case started <- struct{}{}:
		default:
		}
		<-gate
	}

	conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
	session := server.terminal.StartsSnapshot()[0].Session

	writeText(t, conn, map[string]any{"type": "resize", "rows": 40, "cols": 120})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the resize never reached the session")
	}

	writeBinary(t, conn, []byte("x"))
	keystroke := make([]byte, 1)
	if _, err := io.ReadFull(session.Remote, keystroke); err != nil {
		t.Fatalf("read keystroke during a slow resize: %v", err)
	}
	if string(keystroke) != "x" {
		t.Fatalf("keystroke = %q, want %q", keystroke, "x")
	}
	close(gate)
}

// A cross-Origin upgrade with a valid session cookie is refused before any exec.
func TestTerminalSessionRefusesACrossOriginUpgrade(t *testing.T) {
	server, projectID, environmentID, composeID, _ := newTerminalSessionServer(t)

	header := http.Header{}
	header.Set("Origin", "http://evil.example")
	conn, resp, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"), header)
	if conn != nil {
		conn.CloseNow()
	}
	if err == nil {
		t.Fatal("cross-Origin dial succeeded, want refusal")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %v, want status %d", resp, http.StatusForbidden)
	}
	if starts := server.terminal.StartsSnapshot(); len(starts) != 0 {
		t.Fatalf("Starts = %+v, want none", starts)
	}
}

// Bad shell, user or size, and a missing service, are 400 before the upgrade;
// an unknown Compose is 404; a missing cookie is 401.
func TestTerminalSessionValidatesParametersBeforeUpgrade(t *testing.T) {
	server, projectID, environmentID, composeID, _ := newTerminalSessionServer(t)

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"bad shell", "service=app&shell=zsh", http.StatusBadRequest},
		{"bad user", "service=app&user=nobody", http.StatusBadRequest},
		{"zero rows", "service=app&rows=0", http.StatusBadRequest},
		{"large cols", "service=app&cols=501", http.StatusBadRequest},
		{"missing service", "shell=auto", http.StatusBadRequest},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			conn, resp, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, tt.query), nil)
			if conn != nil {
				conn.CloseNow()
			}
			if err == nil {
				t.Fatal("dial succeeded, want a refusal before the upgrade")
			}
			if resp == nil || resp.StatusCode != tt.want {
				t.Fatalf("response = %v, want status %d", resp, tt.want)
			}
		})
	}

	t.Run("unknown compose", func(t *testing.T) {
		conn, resp, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID+1000, "service=app"), nil)
		if conn != nil {
			conn.CloseNow()
		}
		if err == nil {
			t.Fatal("dial succeeded, want 404")
		}
		if resp == nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("response = %v, want status %d", resp, http.StatusNotFound)
		}
	})

	t.Run("no cookie", func(t *testing.T) {
		guest := newTestServer(t)
		conn, resp, err := dialTerminal(t, guest, terminalSessionPath(1, 1, 1, "service=app"), nil)
		if conn != nil {
			conn.CloseNow()
		}
		if err == nil {
			t.Fatal("dial succeeded without a cookie, want 401")
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("response = %v, want status %d", resp, http.StatusUnauthorized)
		}
	})
}

// A Docker API failure reaches the browser as an error frame with Docker's own
// message, unchanged.
func TestTerminalSessionPassesDockerErrorsThrough(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")
	server.terminal.StartErr = errors.New("docker api /containers/x/exec: 409 Conflict: container is paused")

	conn, _, err := dialTerminal(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"), nil)
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	defer conn.CloseNow()
	ctrl := readControl(t, conn, 2*time.Second)
	if ctrl.Type != "error" || ctrl.Message != server.terminal.StartErr.Error() {
		t.Fatalf("control frame = %+v, want error %q", ctrl, server.terminal.StartErr.Error())
	}
	if starts := server.terminal.StartsSnapshot(); len(starts) != 0 {
		t.Fatalf("Starts = %+v, want none", starts)
	}
}
