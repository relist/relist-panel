package httpapi_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/volumes"
	"relist-panel/internal/volumes/faketest"
)

// explorerCompose is the standard Explorer fixture: a Compose whose resolution
// holds one named volume used by two Services, a live bind, a missing bind and
// an unverifiable bind outside the data directory.
type explorerCompose struct {
	project          projectResponse
	production       environmentResponse
	compose          composeResponse
	presentBind      string
	missingBind      string
	unverifiableBind string
}

func setupExplorerCompose(t *testing.T, server *testServer) explorerCompose {
	t.Helper()
	project := createProject(t, server, "Volume Co", "")
	production := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, production.ID, "web")

	present := filepath.Join(server.dataDir, "app", "data")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatalf("create bind source: %v", err)
	}
	missing := filepath.Join(server.dataDir, "app", "retired")

	server.volumeCLI.VolumeExistsByName = map[string]bool{"demoapp_pgdata": true}
	server.volumeCLI.ComposeConfigOutput = []byte(fmt.Sprintf(`{
		"services": {
			"db": {
				"user": "999:998",
				"volumes": [
					{"type": "volume", "source": "pgdata", "target": "/var/lib/postgresql/data"},
					{"type": "bind", "source": %q, "target": "/data"},
					{"type": "bind", "source": %q, "target": "/retired", "read_only": true},
					{"type": "bind", "source": "/srv/media", "target": "/media", "read_only": true}
				]
			},
			"web": {
				"volumes": [
					{"type": "volume", "source": "pgdata", "target": "/pgdata", "read_only": true}
				]
			}
		},
		"volumes": {"pgdata": {"name": "demoapp_pgdata"}}
	}`, present, missing))

	return explorerCompose{
		project:          project,
		production:       production,
		compose:          compose,
		presentBind:      present,
		missingBind:      missing,
		unverifiableBind: "/srv/media",
	}
}

// volumeOpPath builds one operation URL with its query encoded the way the SPA
// sends it.
func volumeOpPath(f explorerCompose, op string, query url.Values) string {
	return composeVolumesPath(f.project.ID, f.production.ID, f.compose.ID) + "/" + op + "?" + query.Encode()
}

func assertErrorContains(t *testing.T, response *http.Response, wants ...string) {
	t.Helper()
	var failure map[string]string
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	for _, want := range wants {
		if !strings.Contains(failure["error"], want) {
			t.Errorf("error = %q, want it to contain %q", failure["error"], want)
		}
	}
}

// Every read operation is exactly one helper run, read-only, against the
// resolved source the request named — for a named volume and a bind alike.
func TestVolumeOperationsRunTheHelperReadOnly(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = `{"entries":[]}`

	tests := []struct {
		name   string
		op     string
		source string
	}{
		{name: "list a named volume", op: "list", source: "demoapp_pgdata"},
		{name: "stat a named volume", op: "stat", source: "demoapp_pgdata"},
		{name: "list a bind", op: "list", source: fixture.presentBind},
		{name: "stat a bind", op: "stat", source: fixture.presentBind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, tt.op, url.Values{"source": {tt.source}}), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusOK, body)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != server.volumeRunner.Stdout {
				t.Errorf("body = %s, want the helper's JSON %s", body, server.volumeRunner.Stdout)
			}
		})
	}

	want := []faketest.Call{
		{Request: volumes.Request{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpList, Path: ".", ReadOnly: true}},
		{Request: volumes.Request{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpStat, Path: ".", ReadOnly: true}},
		{Request: volumes.Request{Source: fixture.presentBind, User: "999:998", Op: volumes.OpList, Path: ".", ReadOnly: true}},
		{Request: volumes.Request{Source: fixture.presentBind, User: "999:998", Op: volumes.OpStat, Path: ".", ReadOnly: true}},
	}
	if !reflect.DeepEqual(server.volumeRunner.Calls, want) {
		t.Errorf("runner calls =\n\t%+v\nwant\n\t%+v", server.volumeRunner.Calls, want)
	}
}

// read streams the helper's bytes through untouched — no JSON, no buffering
// into a wrapper — with the bytes' own content type.
func TestVolumeReadStreamsRawBytes(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = "raw\x00file bytes\n"

	tests := []struct {
		name   string
		source string
	}{
		{name: "named volume", source: "demoapp_pgdata"},
		{name: "bind", source: fixture.presentBind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, "read", url.Values{
				"source": {tt.source},
				"path":   {"sub/file.bin"},
			}), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusOK, body)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/octet-stream" {
				t.Errorf("Content-Type = %q, want application/octet-stream", contentType)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != server.volumeRunner.Stdout {
				t.Errorf("body = %q, want the helper's bytes %q", body, server.volumeRunner.Stdout)
			}
		})
	}

	want := []faketest.Call{
		{Request: volumes.Request{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpRead, Path: "sub/file.bin", ReadOnly: true}},
		{Request: volumes.Request{Source: fixture.presentBind, User: "999:998", Op: volumes.OpRead, Path: "sub/file.bin", ReadOnly: true}},
	}
	if !reflect.DeepEqual(server.volumeRunner.Calls, want) {
		t.Errorf("runner calls =\n\t%+v\nwant\n\t%+v", server.volumeRunner.Calls, want)
	}
}

// A source that is not one of the Compose's resolved Volumes is a 404 and runs
// nothing: a source is never free text (the spec's one load-bearing
// invariant).
func TestVolumeOperationsRefuseUndeclaredSources(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	for _, source := range []string{"", "host-etc", "/etc"} {
		response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{"source": {source}}), nil)
		if response.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Errorf("source %q status = %d, want %d, body = %s", source, response.StatusCode, http.StatusNotFound, body)
			continue
		}
		assertErrorContains(t, response, "unknown volume")
		response.Body.Close()
	}
	if len(server.volumeRunner.Calls) != 0 {
		t.Errorf("runner calls = %+v, want none for an undeclared source", server.volumeRunner.Calls)
	}
}

// A missing source is refused before the mount could create it; an
// unverifiable one proceeds, because the UI has already told the operator that
// continuing creates the path root:root 0755.
func TestVolumeOperationsRefuseMissingSources(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = "{}"

	server.volumeCLI.VolumeExistsByName["demoapp_pgdata"] = false

	named := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{"source": {"demoapp_pgdata"}}), nil)
	defer named.Body.Close()
	if named.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(named.Body)
		t.Fatalf("missing named volume status = %d, want %d, body = %s", named.StatusCode, http.StatusConflict, body)
	}
	assertErrorContains(t, named, "demoapp_pgdata", "does not exist")

	bind := server.request(t, http.MethodGet, volumeOpPath(fixture, "stat", url.Values{"source": {fixture.missingBind}}), nil)
	defer bind.Body.Close()
	if bind.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(bind.Body)
		t.Fatalf("missing bind status = %d, want %d, body = %s", bind.StatusCode, http.StatusConflict, body)
	}
	assertErrorContains(t, bind, fixture.missingBind, "does not exist")

	if len(server.volumeRunner.Calls) != 0 {
		t.Errorf("runner calls = %+v, want none before the unverifiable source", server.volumeRunner.Calls)
	}

	unverifiable := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{"source": {fixture.unverifiableBind}}), nil)
	defer unverifiable.Body.Close()
	if unverifiable.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(unverifiable.Body)
		t.Fatalf("unverifiable bind status = %d, want %d, body = %s", unverifiable.StatusCode, http.StatusOK, body)
	}
	if len(server.volumeRunner.Calls) != 1 || server.volumeRunner.Calls[0].Request.Source != fixture.unverifiableBind {
		t.Errorf("runner calls = %+v, want exactly the unverifiable bind", server.volumeRunner.Calls)
	}
}

// The user precedence is the spec's: explicit user, then the named Service's
// resolved user, then the Volume's first use — and any other value is a 400
// with no run.
func TestVolumeOperationsResolveTheUser(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = "{}"

	tests := []struct {
		name  string
		query url.Values
		user  string
	}{
		{
			name:  "explicit user wins over the named service",
			query: url.Values{"source": {"demoapp_pgdata"}, "user": {"111:222"}, "service": {"web"}},
			user:  "111:222",
		},
		{
			name:  "named service resolves to its compose user",
			query: url.Values{"source": {"demoapp_pgdata"}, "service": {"db"}},
			user:  "999:998",
		},
		{
			name:  "named service without a user resolves to the 0:0 fallback",
			query: url.Values{"source": {"demoapp_pgdata"}, "service": {"web"}},
			user:  "0:0",
		},
		{
			name:  "no service resolves to the volume's first use",
			query: url.Values{"source": {"demoapp_pgdata"}},
			user:  "999:998",
		},
		{
			name:  "a bind resolves through its first use too",
			query: url.Values{"source": {fixture.presentBind}},
			user:  "999:998",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", tt.query), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusOK, body)
			}
			calls := server.volumeRunner.Calls
			if len(calls) == 0 {
				t.Fatal("no runner call recorded")
			}
			if got := calls[len(calls)-1].Request.User; got != tt.user {
				t.Errorf("--user = %q, want %q", got, tt.user)
			}
		})
	}

	refusals := []struct {
		name  string
		query url.Values
		want  string
	}{
		{name: "malformed user", query: url.Values{"source": {"demoapp_pgdata"}, "user": {"root"}}, want: "user must be <uid>:<gid>"},
		{name: "partial user", query: url.Values{"source": {"demoapp_pgdata"}, "user": {"1000:"}}, want: "user must be <uid>:<gid>"},
		{name: "over-long user", query: url.Values{"source": {"demoapp_pgdata"}, "user": {"1:2:3"}}, want: "user must be <uid>:<gid>"},
		{name: "unknown service", query: url.Values{"source": {"demoapp_pgdata"}, "service": {"ghost"}}, want: "unknown service"},
	}
	before := len(server.volumeRunner.Calls)
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", tt.query), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusBadRequest, body)
			}
			assertErrorContains(t, response, tt.want)
		})
	}
	if len(server.volumeRunner.Calls) != before {
		t.Errorf("runner calls = %+v, want none for refused users", server.volumeRunner.Calls[before:])
	}
}

// Paths are relative to the Volume root, one way only: absent or empty is the
// root, an absolute path is a 400 before any run, and everything else — a
// leading `-`, a newline, even `..` for the helper and the kernel to refuse —
// is passed through untouched.
func TestVolumeOperationsInterpretPathsRelativeToTheRoot(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = "{}"

	for _, path := range []string{"-dash", "nested/with\nnewline", "dir/file.txt", ".."} {
		response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{
			"source": {"demoapp_pgdata"},
			"path":   {path},
		}), nil)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("path %q status = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
		last := server.volumeRunner.Calls[len(server.volumeRunner.Calls)-1].Request
		if last.Path != path {
			t.Errorf("path %q recorded as %q, want it untouched", path, last.Path)
		}
	}

	rootPaths := []struct {
		name  string
		query url.Values
	}{
		{name: "absent", query: url.Values{"source": {"demoapp_pgdata"}}},
		{name: "empty", query: url.Values{"source": {"demoapp_pgdata"}, "path": {""}}},
	}
	for _, tt := range rootPaths {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", tt.query), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusOK, body)
			}
			last := server.volumeRunner.Calls[len(server.volumeRunner.Calls)-1].Request
			if last.Path != "." {
				t.Errorf("path = %q, want the volume root %q", last.Path, ".")
			}
		})
	}

	before := len(server.volumeRunner.Calls)
	absolute := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{
		"source": {"demoapp_pgdata"},
		"path":   {"/etc/passwd"},
	}), nil)
	defer absolute.Body.Close()
	if absolute.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(absolute.Body)
		t.Fatalf("absolute path status = %d, want %d, body = %s", absolute.StatusCode, http.StatusBadRequest, body)
	}
	assertErrorContains(t, absolute, "relative to the volume root")
	if len(server.volumeRunner.Calls) != before {
		t.Errorf("runner calls = %+v, want an absolute path refused without a run", server.volumeRunner.Calls[before:])
	}
}

func TestVolumeReadFailureBeforeOutputIsBadGateway(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	server.volumeRunner.Stdout = ""
	server.volumeRunner.Err = errors.New("docker run ... exit status 1: read: permission denied")

	response := server.request(t, http.MethodGet, volumeOpPath(fixture, "read", url.Values{
		"source": {"demoapp_pgdata"},
		"path":   {"secret.txt"},
	}), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusBadGateway, body)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	assertErrorContains(t, response, "volume read failed", "permission denied")
}

func TestVolumeReadFailureAfterBytesLeavesTheResponseCommitted(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	server.volumeRunner.Stdout = "partial bytes"
	server.volumeRunner.Err = errors.New("docker run ... exit status 1: read: input/output error")

	response := server.request(t, http.MethodGet, volumeOpPath(fixture, "read", url.Values{
		"source": {"demoapp_pgdata"},
		"path":   {"big.bin"},
	}), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d: bytes already flowed", response.StatusCode, http.StatusOK)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", contentType)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "partial bytes" {
		t.Errorf("body = %q, want the bytes that got through", body)
	}
}

// An archive is one read-only helper run whose tar.gz bytes reach the client
// untouched, named per RFC 6266 — after the Volume for a whole-Volume
// download, after the archived path's base otherwise — under the spec's
// ten-minute cap. Paths are still passed through untouched, dash and newline
// included.
func TestVolumeArchiveStreamsTheHelperTarGz(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = "\x1f\x8b\x08\x00tar gz bytes\x00\xff"

	tests := []struct {
		name        string
		source      string
		path        string
		disposition string
	}{
		{
			name:        "a whole named volume is named after its logical name",
			source:      "demoapp_pgdata",
			path:        ".",
			disposition: `attachment; filename="pgdata.tar.gz"`,
		},
		{
			name:        "a whole bind is named after its source's base",
			source:      fixture.presentBind,
			path:        ".",
			disposition: `attachment; filename="data.tar.gz"`,
		},
		{
			name:        "a subdirectory is named after the archived path",
			source:      "demoapp_pgdata",
			path:        "backups/nightly",
			disposition: `attachment; filename="nightly.tar.gz"`,
		},
		{
			name:        "a non-ascii name carries a percent-encoded filename*",
			source:      "demoapp_pgdata",
			path:        "backups/café",
			disposition: `attachment; filename="caf_.tar.gz"; filename*=utf-8''caf%c3%a9.tar.gz`,
		},
		{
			name:        "a path beginning with a dash passes through",
			source:      "demoapp_pgdata",
			path:        "-dash",
			disposition: `attachment; filename="-dash.tar.gz"`,
		},
		{
			name:        "a newline is stripped from the header",
			source:      "demoapp_pgdata",
			path:        "nested/with\nnewline",
			disposition: `attachment; filename="withnewline.tar.gz"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, "archive", url.Values{
				"source": {tt.source},
				"path":   {tt.path},
			}), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusOK, body)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/gzip" {
				t.Errorf("Content-Type = %q, want application/gzip", contentType)
			}
			if disposition := response.Header.Get("Content-Disposition"); disposition != tt.disposition {
				t.Errorf("Content-Disposition = %q, want %q", disposition, tt.disposition)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != server.volumeRunner.Stdout {
				t.Errorf("body = %q, want the helper's tar.gz %q", body, server.volumeRunner.Stdout)
			}
		})
	}

	want := []volumes.Request{
		{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpArchive, Path: ".", ReadOnly: true},
		{Source: fixture.presentBind, User: "999:998", Op: volumes.OpArchive, Path: ".", ReadOnly: true},
		{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpArchive, Path: "backups/nightly", ReadOnly: true},
		{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpArchive, Path: "backups/café", ReadOnly: true},
		{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpArchive, Path: "-dash", ReadOnly: true},
		{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpArchive, Path: "nested/with\nnewline", ReadOnly: true},
	}
	calls := server.volumeRunner.Calls
	if len(calls) != len(want) {
		t.Fatalf("runner calls = %d, want %d: %+v", len(calls), len(want), calls)
	}
	for i, request := range want {
		if calls[i].Request != request {
			t.Errorf("call %d request = %+v, want %+v", i, calls[i].Request, request)
		}
		// The cap is the spec's ten minutes, on the run's context.
		if remaining := time.Until(calls[i].Deadline); remaining <= 9*time.Minute || remaining > 10*time.Minute {
			t.Errorf("call %d deadline is %v away, want about 10m", i, remaining)
		}
	}
}

// An archive that fails before producing output is still a 502 carrying the
// helper's message — and a plain JSON error, with no download headers on it.
func TestVolumeArchiveFailureBeforeOutputIsBadGateway(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	server.volumeRunner.Stdout = ""
	server.volumeRunner.Err = errors.New("docker run ... exit status 1: archive: permission denied")

	response := server.request(t, http.MethodGet, volumeOpPath(fixture, "archive", url.Values{
		"source": {"demoapp_pgdata"},
	}), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusBadGateway, body)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		t.Errorf("Content-Disposition = %q, want none before any byte", disposition)
	}
	assertErrorContains(t, response, "volume archive failed", "permission denied")
}

// Once archive bytes have flowed the response is committed: a later failure
// cannot change the status or the headers, only the log.
func TestVolumeArchiveFailureAfterBytesLeavesTheResponseCommitted(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	server.volumeRunner.Stdout = "\x1f\x8b\x08\x00partial gzip"
	server.volumeRunner.Err = errors.New("docker run ... exit status 1: archive: broken pipe")

	response := server.request(t, http.MethodGet, volumeOpPath(fixture, "archive", url.Values{
		"source": {"demoapp_pgdata"},
	}), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d: bytes already flowed", response.StatusCode, http.StatusOK)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", contentType)
	}
	if disposition := response.Header.Get("Content-Disposition"); disposition != `attachment; filename="pgdata.tar.gz"` {
		t.Errorf("Content-Disposition = %q, want the committed disposition", disposition)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "\x1f\x8b\x08\x00partial gzip" {
		t.Errorf("body = %q, want the bytes that got through", body)
	}
}

// The archive route shares the pipeline's refusals: an undeclared source and
// a missing volume both answer before any helper run.
func TestVolumeArchiveRefusesUndeclaredAndMissingSources(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	undeclared := server.request(t, http.MethodGet, volumeOpPath(fixture, "archive", url.Values{"source": {"host-etc"}}), nil)
	defer undeclared.Body.Close()
	if undeclared.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(undeclared.Body)
		t.Fatalf("undeclared source status = %d, want %d, body = %s", undeclared.StatusCode, http.StatusNotFound, body)
	}
	assertErrorContains(t, undeclared, "unknown volume")

	server.volumeCLI.VolumeExistsByName["demoapp_pgdata"] = false
	missing := server.request(t, http.MethodGet, volumeOpPath(fixture, "archive", url.Values{"source": {"demoapp_pgdata"}}), nil)
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(missing.Body)
		t.Fatalf("missing volume status = %d, want %d, body = %s", missing.StatusCode, http.StatusConflict, body)
	}
	assertErrorContains(t, missing, "demoapp_pgdata", "does not exist")

	if len(server.volumeRunner.Calls) != 0 {
		t.Errorf("runner calls = %+v, want none for refused archives", server.volumeRunner.Calls)
	}
}

// `download=1` is read's one extra: the same stream, marked an attachment
// named after the file's base name. Without it the stream stays inline,
// exactly as before, and neither is capped — the ten minutes are the
// archive's.
func TestVolumeReadDownloadSetsAnAttachmentDisposition(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = "raw\x00file bytes"

	download := server.request(t, http.MethodGet, volumeOpPath(fixture, "read", url.Values{
		"source":   {"demoapp_pgdata"},
		"path":     {"sub/file.bin"},
		"download": {"1"},
	}), nil)
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(download.Body)
		t.Fatalf("download status = %d, want %d, body = %s", download.StatusCode, http.StatusOK, body)
	}
	if contentType := download.Header.Get("Content-Type"); contentType != "application/octet-stream" {
		t.Errorf("download Content-Type = %q, want application/octet-stream", contentType)
	}
	if disposition := download.Header.Get("Content-Disposition"); disposition != `attachment; filename="file.bin"` {
		t.Errorf("download Content-Disposition = %q, want the file's base name", disposition)
	}
	body, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if string(body) != server.volumeRunner.Stdout {
		t.Errorf("download body = %q, want the helper's bytes", body)
	}

	inline := server.request(t, http.MethodGet, volumeOpPath(fixture, "read", url.Values{
		"source": {"demoapp_pgdata"},
		"path":   {"sub/file.bin"},
	}), nil)
	defer inline.Body.Close()
	if inline.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(inline.Body)
		t.Fatalf("inline status = %d, want %d, body = %s", inline.StatusCode, http.StatusOK, body)
	}
	if disposition := inline.Header.Get("Content-Disposition"); disposition != "" {
		t.Errorf("plain read Content-Disposition = %q, want none (inline)", disposition)
	}

	want := volumes.Request{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpRead, Path: "sub/file.bin", ReadOnly: true}
	calls := server.volumeRunner.Calls
	if len(calls) != 2 {
		t.Fatalf("runner calls = %d, want 2: %+v", len(calls), calls)
	}
	for i, call := range calls {
		if call.Request != want {
			t.Errorf("call %d request = %+v, want %+v", i, call.Request, want)
		}
		if !call.Deadline.IsZero() {
			t.Errorf("call %d deadline = %v, want none: the ten-minute cap is the archive's", i, call.Deadline)
		}
	}
}

func TestVolumeMetadataFailuresAreBadGateway(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	t.Run("a failed run carries the helper's message", func(t *testing.T) {
		server.volumeRunner.Stdout = ""
		server.volumeRunner.Err = errors.New("docker run ... exit status 1: stat: no such file or directory")

		response := server.request(t, http.MethodGet, volumeOpPath(fixture, "stat", url.Values{
			"source": {"demoapp_pgdata"},
			"path":   {"gone"},
		}), nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadGateway {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusBadGateway, body)
		}
		assertErrorContains(t, response, "volume stat failed", "no such file or directory")
	})

	t.Run("garbage output is refused rather than forwarded", func(t *testing.T) {
		server.volumeRunner.Stdout = "not json at all"
		server.volumeRunner.Err = nil

		response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{"source": {"demoapp_pgdata"}}), nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadGateway {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusBadGateway, body)
		}
		assertErrorContains(t, response, "volume list returned invalid JSON")
	})
}

// Without a containerized panel the feature cannot run, so every operation is
// a 503 with the reason — never a 502 from a run that should not have started.
func TestVolumeOperationsReportTheFeatureUnavailable(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	server.volumeRunner.AvailableErr = errors.New("volume files need a containerized panel whose image the host's Docker can see")

	for _, op := range []string{"list", "stat", "read", "archive"} {
		t.Run(op, func(t *testing.T) {
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, op, url.Values{"source": {"demoapp_pgdata"}}), nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, http.StatusServiceUnavailable, body)
			}
			assertErrorContains(t, response, "volume files unavailable", "containerized panel")
		})
	}

	if len(server.volumeCLI.ComposeConfigRefs) != 0 {
		t.Errorf("ComposeConfig calls = %+v, want none while unavailable", server.volumeCLI.ComposeConfigRefs)
	}
	if len(server.volumeRunner.Calls) != 0 {
		t.Errorf("runner calls = %+v, want none while unavailable", server.volumeRunner.Calls)
	}
}

// The operation routes are ordinary children of the Compose routes: an unknown
// Compose (or parent) is a 404 and nothing runs.
func TestVolumeOperationsRequireAKnownCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)

	paths := []string{
		composeVolumesPath(fixture.project.ID, fixture.production.ID, 999) + "/list?source=demoapp_pgdata",
		composeVolumesPath(fixture.project.ID, 999, fixture.compose.ID) + "/stat?source=demoapp_pgdata",
		composeVolumesPath(999, fixture.production.ID, fixture.compose.ID) + "/read?source=demoapp_pgdata",
	}
	for _, path := range paths {
		response := server.request(t, http.MethodGet, path, nil)
		if response.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Errorf("%s status = %d, want %d, body = %s", path, response.StatusCode, http.StatusNotFound, body)
			continue
		}
		response.Body.Close()
	}
	if len(server.volumeCLI.ComposeConfigRefs) != 0 {
		t.Errorf("ComposeConfig calls = %+v, want none without a known compose", server.volumeCLI.ComposeConfigRefs)
	}
}
