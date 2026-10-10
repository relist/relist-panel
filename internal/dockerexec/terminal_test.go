package dockerexec_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/terminal"
)

// newTerminalDaemon serves handler over a real unix socket in t.TempDir(),
// the transport the panel uses in production, without a real daemon. The
// temporary server starts on an ephemeral TCP port first; that listener is
// closed and replaced with the unix one before the server starts serving.
func newTerminalDaemon(t *testing.T, handler http.Handler) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return socket
}

func writeTerminalJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestTerminalExecServiceContainersFiltersByComposeLabels(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath, gotQuery string
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		mu.Unlock()
		_, _ = w.Write([]byte(`[
			{"Id":"id2","Names":["/relist-c3-app-2"],"Labels":{"com.docker.compose.container-number":"2"}},
			{"Id":"id1","Names":["/relist-c3-app-1"],"Labels":{"com.docker.compose.container-number":"1"}}
		]`))
	}))

	containers, err := dockerexec.NewTerminalExec(socket, "").ServiceContainers(context.Background(), "relist-c3", "app")
	if err != nil {
		t.Fatalf("ServiceContainers() error = %v", err)
	}

	mu.Lock()
	method, path, query := gotMethod, gotPath, gotQuery
	mu.Unlock()
	if method != http.MethodGet || path != "/containers/json" {
		t.Errorf("request was %s %s, want GET /containers/json", method, path)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	var filters map[string][]string
	if err := json.Unmarshal([]byte(values.Get("filters")), &filters); err != nil {
		t.Fatalf("filters %q is not JSON: %v", values.Get("filters"), err)
	}
	wantFilters := map[string][]string{
		"label": {
			"com.docker.compose.project=relist-c3",
			"com.docker.compose.service=app",
		},
	}
	if !reflect.DeepEqual(filters, wantFilters) {
		t.Errorf("filters = %v, want %v", filters, wantFilters)
	}

	want := []terminal.Container{
		{ID: "id1", Name: "relist-c3-app-1", Number: 1},
		{ID: "id2", Name: "relist-c3-app-2", Number: 2},
	}
	if !reflect.DeepEqual(containers, want) {
		t.Errorf("ServiceContainers() = %+v, want %+v", containers, want)
	}
}

func TestTerminalExecHasShellReadsTheExitCode(t *testing.T) {
	ctx := context.Background()

	t.Run("a zero exit code means the shell is there", func(t *testing.T) {
		var mu sync.Mutex
		var createBody []byte
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			createBody = body
			mu.Unlock()
			writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
		})
		mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExitCode": 0})
		})
		socket := newTerminalDaemon(t, mux)

		hasShell, err := dockerexec.NewTerminalExec(socket, "").HasShell(ctx, "c1", "/bin/bash")
		if err != nil {
			t.Fatalf("HasShell() error = %v", err)
		}
		if !hasShell {
			t.Error("HasShell() = false, want true for a zero exit code")
		}

		mu.Lock()
		body := createBody
		mu.Unlock()
		var create struct {
			Cmd []string `json:"Cmd"`
			Tty bool     `json:"Tty"`
		}
		if err := json.Unmarshal(body, &create); err != nil {
			t.Fatalf("create body %q is not JSON: %v", body, err)
		}
		if want := []string{"/bin/bash", "-c", "exit 0"}; !reflect.DeepEqual(create.Cmd, want) {
			t.Errorf("create Cmd = %v, want %v", create.Cmd, want)
		}
		if create.Tty {
			t.Error("create Tty = true, want false")
		}
	})

	t.Run("a missing executable is not a shell", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusNotFound, map[string]string{
				"message": `exec: "/bin/bash": executable file not found in $PATH`,
			})
		})
		socket := newTerminalDaemon(t, mux)

		hasShell, err := dockerexec.NewTerminalExec(socket, "").HasShell(ctx, "c1", "/bin/bash")
		if err != nil {
			t.Fatalf("HasShell() error = %v, want nil", err)
		}
		if hasShell {
			t.Error("HasShell() = true, want false")
		}
	})

	t.Run("a start that cannot find the executable is not a shell", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
		})
		mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusInternalServerError, map[string]string{
				"message": `OCI runtime exec failed: exec: "/bin/bash": executable file not found in $PATH: unknown`,
			})
		})
		socket := newTerminalDaemon(t, mux)

		hasShell, err := dockerexec.NewTerminalExec(socket, "").HasShell(ctx, "c1", "/bin/bash")
		if err != nil {
			t.Fatalf("HasShell() error = %v, want nil", err)
		}
		if hasShell {
			t.Error("HasShell() = true, want false")
		}
	})

	t.Run("a non-zero exit code is not a shell", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
		})
		mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExitCode": 126})
		})
		socket := newTerminalDaemon(t, mux)

		hasShell, err := dockerexec.NewTerminalExec(socket, "").HasShell(ctx, "c1", "/bin/bash")
		if err != nil {
			t.Fatalf("HasShell() error = %v, want nil", err)
		}
		if hasShell {
			t.Error("HasShell() = true, want false")
		}
	})

	t.Run("a server error is an error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusInternalServerError, map[string]string{"message": "boom"})
		})
		socket := newTerminalDaemon(t, mux)

		_, err := dockerexec.NewTerminalExec(socket, "").HasShell(ctx, "c1", "/bin/bash")
		if err == nil {
			t.Fatal("HasShell() = nil error for a 500, want an error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("HasShell() error = %v, want it to mention 500", err)
		}
	})
}

func TestTerminalExecStartHijacksATtyStream(t *testing.T) {
	var mu sync.Mutex
	var creates [][]byte
	type startRequest struct {
		header http.Header
		body   []byte
	}
	var starts []startRequest

	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{"Config": map[string]any{"Env": []string{"PATH=/usr/bin"}}})
	})
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		creates = append(creates, body)
		mu.Unlock()
		writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
	})
	mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		starts = append(starts, startRequest{header: r.Header.Clone(), body: body})
		mu.Unlock()

		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n"); err != nil {
			t.Errorf("write 101: %v", err)
			return
		}
		if err := rw.Flush(); err != nil {
			t.Errorf("flush 101: %v", err)
			return
		}
		line, err := rw.ReadString('\n')
		if err != nil {
			// A session closed without sending anything is expected for the
			// root-user connection, which the test only inspects the create
			// body of.
			return
		}
		if _, err := rw.WriteString(line); err != nil {
			t.Errorf("echo: %v", err)
			return
		}
		if err := rw.Flush(); err != nil {
			t.Errorf("flush echo: %v", err)
			return
		}
		_, _ = io.Copy(io.Discard, conn)
	})
	socket := newTerminalDaemon(t, mux)
	exec := dockerexec.NewTerminalExec(socket, "")

	defaultSession, err := exec.Start(context.Background(), "c1", terminal.ExecOptions{
		Shell: "/bin/bash", User: "", Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = defaultSession.Close() })

	if _, err := defaultSession.Write([]byte("hello\n")); err != nil {
		t.Fatalf("session Write() error = %v", err)
	}
	echoed, err := bufio.NewReader(defaultSession).ReadString('\n')
	if err != nil {
		t.Fatalf("session Read() error = %v", err)
	}
	if echoed != "hello\n" {
		t.Errorf("session echoed %q, want %q", echoed, "hello\n")
	}

	rootSession, err := exec.Start(context.Background(), "c1", terminal.ExecOptions{
		Shell: "/bin/sh", User: "0", Rows: 40, Cols: 120,
	})
	if err != nil {
		t.Fatalf("Start() with user 0 error = %v", err)
	}
	t.Cleanup(func() { _ = rootSession.Close() })

	mu.Lock()
	createA, createB := creates[0], creates[1]
	startA, startB := starts[0], starts[1]
	mu.Unlock()

	var first struct {
		Tty          bool     `json:"Tty"`
		AttachStdin  bool     `json:"AttachStdin"`
		AttachStdout bool     `json:"AttachStdout"`
		AttachStderr bool     `json:"AttachStderr"`
		User         string   `json:"User"`
		Env          []string `json:"Env"`
		ConsoleSize  []uint16 `json:"ConsoleSize"`
	}
	if err := json.Unmarshal(createA, &first); err != nil {
		t.Fatalf("create body %q is not JSON: %v", createA, err)
	}
	if !first.Tty || !first.AttachStdin || !first.AttachStdout || !first.AttachStderr {
		t.Errorf("create body = %s, want Tty and all three Attach* true", createA)
	}
	if first.User != "" {
		t.Errorf("create User = %q, want empty", first.User)
	}
	if strings.Contains(string(createA), `"User"`) {
		t.Errorf("create body = %s, want User omitted for the image default", createA)
	}
	if want := []string{"TERM=xterm-256color", "LANG=C.UTF-8"}; !reflect.DeepEqual(first.Env, want) {
		t.Errorf("create Env = %v, want %v", first.Env, want)
	}
	if want := []uint16{24, 80}; !reflect.DeepEqual(first.ConsoleSize, want) {
		t.Errorf("create ConsoleSize = %v, want %v", first.ConsoleSize, want)
	}

	var second struct {
		User        string   `json:"User"`
		ConsoleSize []uint16 `json:"ConsoleSize"`
	}
	if err := json.Unmarshal(createB, &second); err != nil {
		t.Fatalf("create body %q is not JSON: %v", createB, err)
	}
	if second.User != "0" {
		t.Errorf("create User = %q, want %q", second.User, "0")
	}
	if !strings.Contains(string(createB), `"User":"0"`) {
		t.Errorf("create body = %s, want User \"0\" kept", createB)
	}
	if want := []uint16{40, 120}; !reflect.DeepEqual(second.ConsoleSize, want) {
		t.Errorf("create ConsoleSize = %v, want %v", second.ConsoleSize, want)
	}

	for i, start := range []startRequest{startA, startB} {
		if got := start.header.Get("Upgrade"); got != "tcp" {
			t.Errorf("start %d Upgrade = %q, want tcp", i, got)
		}
		if got := strings.ToLower(start.header.Get("Connection")); !strings.Contains(got, "upgrade") {
			t.Errorf("start %d Connection = %q, want upgrade", i, got)
		}
		if got, want := string(start.body), `{"Detach":false,"Tty":true}`; got != want {
			t.Errorf("start %d body = %s, want %s", i, got, want)
		}
	}
}

// The image's own locale stands: any non-empty LANG, LC_ALL or LC_CTYPE in
// Config.Env keeps the exec from adding LANG=C.UTF-8. An empty one does not
// count, since it chooses nothing.
func TestTerminalExecStartKeepsTheImageLocale(t *testing.T) {
	cases := []struct {
		name    string
		env     []string
		wantEnv []string
	}{
		{"LANG", []string{"LANG=en_US.UTF-8"}, []string{"TERM=xterm-256color"}},
		{"LC_ALL", []string{"LC_ALL=th_TH.UTF-8"}, []string{"TERM=xterm-256color"}},
		{"LC_CTYPE", []string{"LC_CTYPE=C.UTF-8"}, []string{"TERM=xterm-256color"}},
		{"empty LANG", []string{"LANG="}, []string{"TERM=xterm-256color", "LANG=C.UTF-8"}},
		{"no env", nil, []string{"TERM=xterm-256color", "LANG=C.UTF-8"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var create []byte
			var inspectPath string
			mux := http.NewServeMux()
			mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				inspectPath = r.URL.Path
				mu.Unlock()
				writeTerminalJSON(t, w, http.StatusOK, map[string]any{"Config": map[string]any{"Env": tc.env}})
			})
			mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				create = body
				mu.Unlock()
				writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
			})
			mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
				conn, rw, err := http.NewResponseController(w).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				defer conn.Close()
				_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
				_ = rw.Flush()
				_, _ = io.Copy(io.Discard, conn)
			})
			socket := newTerminalDaemon(t, mux)

			session, err := dockerexec.NewTerminalExec(socket, "").Start(context.Background(), "c1", terminal.ExecOptions{Rows: 24, Cols: 80})
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			t.Cleanup(func() { _ = session.Close() })

			mu.Lock()
			gotCreate, gotInspect := create, inspectPath
			mu.Unlock()
			if gotInspect != "/containers/c1/json" {
				t.Errorf("inspect path = %q, want /containers/c1/json", gotInspect)
			}
			var body struct {
				Env []string `json:"Env"`
			}
			if err := json.Unmarshal(gotCreate, &body); err != nil {
				t.Fatalf("create body %q is not JSON: %v", gotCreate, err)
			}
			if !reflect.DeepEqual(body.Env, tc.wantEnv) {
				t.Errorf("create Env = %v, want %v", body.Env, tc.wantEnv)
			}
		})
	}
}

// A failed inspect fails Start before any exec is created: the daemon cannot
// see the Container, so the create would fail too.
func TestTerminalExecStartFailsOnAFailedInspect(t *testing.T) {
	var mu sync.Mutex
	created := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"No such container: c1"}`, http.StatusNotFound)
	})
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		created = true
		mu.Unlock()
		writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
	})
	socket := newTerminalDaemon(t, mux)

	_, err := dockerexec.NewTerminalExec(socket, "").Start(context.Background(), "c1", terminal.ExecOptions{Rows: 24, Cols: 80})
	if err == nil {
		t.Fatal("Start() error = nil, want the inspect's 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("Start() error = %v, want it to mention 404", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if created {
		t.Error("exec was created after a failed inspect")
	}
}

func TestTerminalExecSessionResizeAndExitCode(t *testing.T) {
	var mu sync.Mutex
	var resizeMethod, resizePath, resizeQuery string
	var inspectMethod, inspectPath string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{"Config": map[string]any{}})
	})
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
	})
	mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, conn)
	})
	mux.HandleFunc("POST /exec/{id}/resize", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		resizeMethod, resizePath, resizeQuery = r.Method, r.URL.Path, r.URL.RawQuery
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inspectMethod, inspectPath = r.Method, r.URL.Path
		mu.Unlock()
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ID": "e1", "ExitCode": 7, "Running": false})
	})
	socket := newTerminalDaemon(t, mux)
	exec := dockerexec.NewTerminalExec(socket, "")

	session, err := exec.Start(context.Background(), "c1", terminal.ExecOptions{Shell: "/bin/bash", Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if err := session.Resize(context.Background(), 40, 120); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	mu.Lock()
	gotMethod, gotPath, gotQuery := resizeMethod, resizePath, resizeQuery
	mu.Unlock()
	if gotMethod != http.MethodPost || gotPath != "/exec/e1/resize" || gotQuery != "h=40&w=120" {
		t.Errorf("resize request was %s %s?%s, want POST /exec/e1/resize?h=40&w=120", gotMethod, gotPath, gotQuery)
	}

	code, err := session.ExitCode(context.Background())
	if err != nil {
		t.Fatalf("ExitCode() error = %v", err)
	}
	if code != 7 {
		t.Errorf("ExitCode() = %d, want 7", code)
	}
	mu.Lock()
	gotMethod, gotPath = inspectMethod, inspectPath
	mu.Unlock()
	if gotMethod != http.MethodGet || gotPath != "/exec/e1/json" {
		t.Errorf("inspect request was %s %s, want GET /exec/e1/json", gotMethod, gotPath)
	}
}

// Kill signals a pid through the shell's builtin, not an executable: a non-TTY
// exec of `/bin/sh -c "kill -<sig> <pid>"` as root. A non-zero exit is the pid
// already gone, terminal.ErrNoProcess.
func TestTerminalExecKillUsesTheShellBuiltinAsRoot(t *testing.T) {
	ctx := context.Background()

	t.Run("the kill is a non-tty shell builtin as root", func(t *testing.T) {
		var mu sync.Mutex
		var createBody []byte
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			createBody = body
			mu.Unlock()
			writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
		})
		mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExitCode": 0})
		})
		socket := newTerminalDaemon(t, mux)

		if err := dockerexec.NewTerminalExec(socket, "").Kill(ctx, "c1", 4242, "HUP"); err != nil {
			t.Fatalf("Kill() error = %v", err)
		}

		mu.Lock()
		body := createBody
		mu.Unlock()
		var create struct {
			Cmd  []string `json:"Cmd"`
			User string   `json:"User"`
			Tty  bool     `json:"Tty"`
		}
		if err := json.Unmarshal(body, &create); err != nil {
			t.Fatalf("create body %q is not JSON: %v", body, err)
		}
		if want := []string{"/bin/sh", "-c", "kill -HUP 4242"}; !reflect.DeepEqual(create.Cmd, want) {
			t.Errorf("create Cmd = %v, want %v", create.Cmd, want)
		}
		if create.User != "0" {
			t.Errorf("create User = %q, want %q", create.User, "0")
		}
		if create.Tty {
			t.Error("create Tty = true, want false")
		}
	})

	t.Run("a non-zero exit is no such process", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
		})
		mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExitCode": 1})
		})
		socket := newTerminalDaemon(t, mux)

		err := dockerexec.NewTerminalExec(socket, "").Kill(ctx, "c1", 4242, "0")
		if !errors.Is(err, terminal.ErrNoProcess) {
			t.Fatalf("Kill() error = %v, want terminal.ErrNoProcess", err)
		}
	})
}

func TestTerminalExecAvailablePingsTheDaemon(t *testing.T) {
	var mu sync.Mutex
	var method, path string
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		method, path = r.Method, r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))

	if err := dockerexec.NewTerminalExec(socket, "").Available(context.Background()); err != nil {
		t.Fatalf("Available() error = %v", err)
	}
	mu.Lock()
	gotMethod, gotPath := method, path
	mu.Unlock()
	if gotMethod != http.MethodGet || gotPath != "/_ping" {
		t.Errorf("request was %s %s, want GET /_ping", gotMethod, gotPath)
	}

	missing := filepath.Join(t.TempDir(), "absent.sock")
	err := dockerexec.NewTerminalExec(missing, "").Available(context.Background())
	if err == nil {
		t.Fatal("Available() = nil error for an absent socket, want an error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("Available() error = %v, want it to name %q", err, missing)
	}
}

// OrphanShells finds the marked shells a crashed panel left behind: for each
// running Container of one of the named projects it reads the Container's
// ExecIDs, keeps the execs that are Running and carry the wrapper marker at
// ProcessConfig.arguments[2], and maps each exec's host pid to the
// container-namespace pid — the last field of the mounted proc's NSpid line.
// An exec whose status file has vanished is skipped: it is already gone. A
// Container or exec that answers 404 (removed between the list and its inspect)
// is skipped too, and does not abort the sweep: another orphan is still found.
func TestTerminalExecOrphanShellsFindsRunningMarkedExecs(t *testing.T) {
	procRoot := t.TempDir()
	writeProcStatus(t, procRoot, 111, "Name:\tsh\nPid:\t111\nNSpid:\t111\t42\t7\n")

	var mu sync.Mutex
	var inspected []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/json", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusOK, []map[string]any{
			{"Id": "web", "Labels": map[string]string{"com.docker.compose.project": "relist-c3"}},
			{"Id": "vanished", "Labels": map[string]string{"com.docker.compose.project": "relist-c3"}},
			{"Id": "other", "Labels": map[string]string{"com.docker.compose.project": "not-ours"}},
		})
	})
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inspected = append(inspected, r.PathValue("id"))
		mu.Unlock()
		switch r.PathValue("id") {
		case "vanished":
			// The Container was removed between the list and this inspect — a
			// redeploy race — so it must be skipped, not abort the sweep.
			writeTerminalJSON(t, w, http.StatusNotFound, map[string]string{"message": "No such container"})
		case "web":
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{
				"ExecIDs": []string{"e-running", "e-exited", "e-other", "e-missing", "e-vanished"},
			})
		default:
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExecIDs": []string{"e-foreign"}})
		}
	})
	mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		marker := []string{"-c", `echo $$; exec "$1"`, "relist-terminal", "/bin/sh"}
		switch r.PathValue("id") {
		case "e-running":
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{
				"Running": true, "Pid": 111,
				"ProcessConfig": map[string]any{"entrypoint": "/bin/sh", "arguments": marker},
			})
		case "e-exited":
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{
				"Running": false, "Pid": 333,
				"ProcessConfig": map[string]any{"arguments": marker},
			})
		case "e-other":
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{
				"Running": true, "Pid": 444,
				"ProcessConfig": map[string]any{"arguments": []string{"-c", "sleep 1001"}},
			})
		case "e-missing":
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{
				"Running": true, "Pid": 222,
				"ProcessConfig": map[string]any{"arguments": marker},
			})
		case "e-vanished":
			// The exec was removed between the Container's ExecIDs list and
			// this inspect — the same redeploy race — so it is skipped too.
			writeTerminalJSON(t, w, http.StatusNotFound, map[string]string{"message": "No such exec"})
		default:
			t.Errorf("unexpected exec inspect %q", r.PathValue("id"))
			writeTerminalJSON(t, w, http.StatusNotFound, map[string]string{"message": "no such exec"})
		}
	})
	socket := newTerminalDaemon(t, mux)

	orphans, err := dockerexec.NewTerminalExec(socket, procRoot).OrphanShells(context.Background(), []string{"relist-c3"})
	if err != nil {
		t.Fatalf("OrphanShells() error = %v", err)
	}

	want := []terminal.OrphanShell{{ContainerID: "web", PID: 7}}
	if !reflect.DeepEqual(orphans, want) {
		t.Errorf("OrphanShells() = %+v, want %+v", orphans, want)
	}

	mu.Lock()
	gotInspected := append([]string(nil), inspected...)
	mu.Unlock()
	for _, id := range gotInspected {
		if id != "web" && id != "vanished" {
			t.Errorf("inspected Container %q; only known project labels may be swept", id)
		}
	}
	if !reflect.DeepEqual(gotInspected, []string{"web", "vanished"}) {
		t.Errorf("inspected = %v, want web and vanished", gotInspected)
	}
}

// One container whose inspect fails must not hide the shells of the others:
// OrphanShells returns what it found, plus an error naming what failed.
func TestTerminalExecOrphanShellsContinuesPastAFailedInspect(t *testing.T) {
	procRoot := t.TempDir()
	writeProcStatus(t, procRoot, 111, "Name:\tsh\nPid:\t111\nNSpid:\t111\t42\t7\n")

	marker := []string{"-c", `echo $$; exec "$1"`, "relist-terminal", "/bin/sh"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/json", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusOK, []map[string]any{
			{"Id": "broken", "Labels": map[string]string{"com.docker.compose.project": "relist-c3"}},
			{"Id": "web", "Labels": map[string]string{"com.docker.compose.project": "relist-c3"}},
		})
	})
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "broken" {
			writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExecIDs": []string{"e-broken"}})
			return
		}
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{"ExecIDs": []string{"e-running"}})
	})
	mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "e-broken" {
			writeTerminalJSON(t, w, http.StatusInternalServerError, map[string]string{"message": "boom"})
			return
		}
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{
			"Running": true, "Pid": 111,
			"ProcessConfig": map[string]any{"arguments": marker},
		})
	})
	socket := newTerminalDaemon(t, mux)

	orphans, err := dockerexec.NewTerminalExec(socket, procRoot).OrphanShells(context.Background(), []string{"relist-c3"})

	want := []terminal.OrphanShell{{ContainerID: "web", PID: 7}}
	if !reflect.DeepEqual(orphans, want) {
		t.Errorf("OrphanShells() = %+v, want %+v", orphans, want)
	}
	if err == nil || !strings.Contains(err.Error(), "/exec/e-broken/json") {
		t.Errorf("OrphanShells() error = %v, want one naming /exec/e-broken/json", err)
	}
}

// writeProcStatus writes a golden <procRoot>/<pid>/status, the host proc entry
// the sweep reads an exec's NSpid line from.
func writeProcStatus(t *testing.T, procRoot string, pid int, content string) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s/status: %v", dir, err)
	}
}

// scriptedInspect answers GET /exec/{id}/json from answers in order, repeating
// the last one, the way a daemon reports a finished exec a moment late.
func scriptedInspect(answers ...string) http.HandlerFunc {
	var mu sync.Mutex
	next := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		answer := answers[min(next, len(answers)-1)]
		next++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, answer)
	}
}

// plainStartMux is a daemon whose non-TTY exec starts fine and whose inspect
// follows answers.
func plainStartMux(t *testing.T, answers ...string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
	})
	mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /exec/{id}/json", scriptedInspect(answers...))
	return mux
}

// hijackingStartMux is the same daemon for a TTY exec: the start hijacks.
func hijackingStartMux(t *testing.T, answers ...string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{"Config": map[string]any{}})
	})
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
	})
	mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, conn)
	})
	mux.HandleFunc("GET /exec/{id}/json", scriptedInspect(answers...))
	return mux
}

// The daemon can report a finished stream while the exec still reads
// Running with a null ExitCode; the adapter waits for the real code.
func TestTerminalExecExitCodeWaitsForTheDaemon(t *testing.T) {
	socket := newTerminalDaemon(t, hijackingStartMux(t,
		`{"Running":true,"ExitCode":null}`,
		`{"Running":true,"ExitCode":null}`,
		`{"Running":false,"ExitCode":3}`,
	))
	session, err := dockerexec.NewTerminalExec(socket, "").Start(context.Background(), "c1", terminal.ExecOptions{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	code, err := session.ExitCode(context.Background())
	if err != nil {
		t.Fatalf("ExitCode() error = %v", err)
	}
	if code != 3 {
		t.Errorf("ExitCode() = %d, want 3", code)
	}
}

func TestTerminalExecExitCodeGivesUpWithTheContext(t *testing.T) {
	socket := newTerminalDaemon(t, hijackingStartMux(t, `{"Running":true,"ExitCode":null}`))
	session, err := dockerexec.NewTerminalExec(socket, "").Start(context.Background(), "c1", terminal.ExecOptions{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = session.ExitCode(ctx)
	if err == nil || !strings.Contains(err.Error(), "exit code not reported") {
		t.Fatalf("ExitCode() error = %v, want one containing %q", err, "exit code not reported")
	}
	if !strings.Contains(err.Error(), "/exec/e1/json") {
		t.Errorf("ExitCode() error = %v, want it to name /exec/e1/json", err)
	}
}

func TestTerminalExecHasShellWaitsForTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name string
		last string
		want bool
	}{
		{"zero after a null code is a shell", `{"Running":false,"ExitCode":0}`, true},
		{"127 after a null code is not a shell", `{"Running":false,"ExitCode":127}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := plainStartMux(t, `{"Running":true,"ExitCode":null}`, tc.last)
			socket := newTerminalDaemon(t, mux)
			got, err := dockerexec.NewTerminalExec(socket, "").HasShell(context.Background(), "c1", "/bin/bash")
			if err != nil {
				t.Fatalf("HasShell() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("HasShell() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTerminalExecKillWaitsForTheDaemon(t *testing.T) {
	mux := plainStartMux(t, `{"Running":true,"ExitCode":null}`, `{"Running":false,"ExitCode":1}`)
	socket := newTerminalDaemon(t, mux)
	err := dockerexec.NewTerminalExec(socket, "").Kill(context.Background(), "c1", 4242, "0")
	if !errors.Is(err, terminal.ErrNoProcess) {
		t.Fatalf("Kill() error = %v, want terminal.ErrNoProcess", err)
	}
}
