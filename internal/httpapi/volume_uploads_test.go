package httpapi_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/volumes"
	"relist-panel/internal/volumes/faketest"
)

// plentyFree is the free op's answer when space is not under test.
const plentyFree = `{"bytes": 1099511627776}`

type uploadStart struct {
	ID        string `json:"id"`
	ChunkSize int64  `json:"chunkSize"`
	Offset    int64  `json:"offset"`
}

type uploadView struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	LastModified int64  `json:"lastModified"`
	Offset       int64  `json:"offset"`
	State        string `json:"state"`
	ErrorKind    string `json:"errorKind"`
	Error        string `json:"error"`
	UpdatedAt    string `json:"updatedAt"`
}

func uploadsBase(f explorerCompose) string {
	return composeVolumesPath(f.project.ID, f.production.ID, f.compose.ID) + "/uploads"
}

func uploadsQuery(path string) url.Values {
	return url.Values{"source": {"demoapp_pgdata"}, "path": {path}, "service": {"db"}, "rootConfirmed": {"1"}, "bindConfirmed": {"1"}}
}

func startUpload(t *testing.T, server *testServer, base, path string, size int64) *http.Response {
	t.Helper()
	return server.request(t, http.MethodPost, base+"?"+uploadsQuery(path).Encode(), map[string]any{
		"name": "notes.bin", "size": size, "lastModified": 42,
	})
}

func mustStartUpload(t *testing.T, server *testServer, base, path string, size int64) uploadStart {
	t.Helper()
	response := startUpload(t, server, base, path, size)
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("start = %d, body = %s", response.StatusCode, body)
	}
	var started uploadStart
	decodeJSON(t, response, &started)
	return started
}

func patchChunk(t *testing.T, server *testServer, base, id string, offset int64, chunk []byte, length int64) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, "http://relist-panel.test"+base+"/"+id, bytes.NewReader(chunk))
	request.ContentLength = length
	if length < 0 {
		request.Body = io.NopCloser(struct{ io.Reader }{bytes.NewReader(chunk)})
	}
	request.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	return server.serve(request)
}

func completeUpload(t *testing.T, server *testServer, base, id string) (int, uploadView) {
	t.Helper()
	response := server.request(t, http.MethodPost, base+"/"+id+"/complete", nil)
	var view uploadView
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted {
		decodeJSON(t, response, &view)
	} else {
		body, _ := io.ReadAll(response.Body)
		view.Error = string(body)
	}
	return response.StatusCode, view
}

func writeCalls(calls []faketest.Call) []faketest.Call {
	var out []faketest.Call
	for _, c := range calls {
		if c.Request.Op == volumes.OpWrite {
			out = append(out, c)
		}
	}
	return out
}

func TestUploadRoundTrip(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = plentyFree
	base := uploadsBase(fixture)

	started := mustStartUpload(t, server, base, "uploads/notes.bin", 5)
	if started.ChunkSize != 33554432 || started.Offset != 0 || len(started.ID) != 32 {
		t.Fatalf("start = %+v", started)
	}
	free := server.volumeRunner.Snapshot()
	if len(free) != 1 || free[0].Request != (volumes.Request{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpFree, Path: ".", ReadOnly: true}) {
		t.Fatalf("start runs = %+v, want one read-only free", free)
	}

	first := patchChunk(t, server, base, started.ID, 0, []byte("hel"), 3)
	if first.StatusCode != http.StatusNoContent || first.Header.Get("Upload-Offset") != "3" {
		t.Fatalf("first chunk = %d, offset %q", first.StatusCode, first.Header.Get("Upload-Offset"))
	}
	second := patchChunk(t, server, base, started.ID, 3, []byte("lo"), 2)
	if second.StatusCode != http.StatusNoContent || second.Header.Get("Upload-Offset") != "5" {
		t.Fatalf("second chunk = %d, offset %q", second.StatusCode, second.Header.Get("Upload-Offset"))
	}

	status, view := completeUpload(t, server, base, started.ID)
	if status != http.StatusOK || view.State != "done" {
		t.Fatalf("complete = %d %+v", status, view)
	}
	writes := writeCalls(server.volumeRunner.Snapshot())
	want := faketest.Call{Request: volumes.Request{Source: "demoapp_pgdata", User: "999:998", Op: volumes.OpWrite, Path: "uploads/notes.bin", DeclaredBytes: 5}, Stdin: []byte("hello")}
	if len(writes) != 1 || writes[0].Request != want.Request || string(writes[0].Stdin) != "hello" {
		t.Fatalf("writes = %+v, want %+v", writes, want)
	}

	response := server.request(t, http.MethodGet, base+"/"+started.ID, nil)
	var got uploadView
	decodeJSON(t, response, &got)
	if got.State != "done" || got.Source != "demoapp_pgdata" || got.Path != "uploads/notes.bin" || got.Name != "notes.bin" || got.Size != 5 || got.Offset != 5 || got.LastModified != 42 {
		t.Fatalf("status = %+v", got)
	}
}

func TestUploadStartChecks(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	base := uploadsBase(fixture)
	server.volumeRunner.Stdout = plentyFree

	over := startUpload(t, server, base, "big.bin", 10<<30+1)
	if over.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over cap = %d", over.StatusCode)
	}
	assertErrorContains(t, over, "10 GiB")

	server.volumeRunner.Stdout = `{"bytes": 9}`
	tight := startUpload(t, server, base, "x.bin", 5)
	if tight.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("volume too small = %d", tight.StatusCode)
	}
	assertErrorContains(t, tight, "10", "9")
	server.volumeRunner.Stdout = plentyFree

	unknown := server.request(t, http.MethodPost, base+"?source=nope&path=x.bin&service=db&rootConfirmed=1&bindConfirmed=1", map[string]any{"name": "x.bin", "size": 5, "lastModified": 1})
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown source = %d", unknown.StatusCode)
	}

	for i := 0; i < 3; i++ {
		mustStartUpload(t, server, base, "f"+strconv.Itoa(i)+".bin", 5)
	}
	if fourth := startUpload(t, server, base, "f3.bin", 5); fourth.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4th open upload = %d", fourth.StatusCode)
	}
}

func TestUploadPatchErrors(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = plentyFree
	base := uploadsBase(fixture)
	started := mustStartUpload(t, server, base, "x.bin", 5)

	wrong := patchChunk(t, server, base, started.ID, 2, []byte("he"), 2)
	if wrong.StatusCode != http.StatusConflict || wrong.Header.Get("Upload-Offset") != "0" {
		t.Fatalf("wrong offset = %d, Upload-Offset %q", wrong.StatusCode, wrong.Header.Get("Upload-Offset"))
	}
	if chunked := patchChunk(t, server, base, started.ID, 0, []byte("he"), -1); chunked.StatusCode != http.StatusLengthRequired {
		t.Fatalf("no length = %d", chunked.StatusCode)
	}
	if huge := patchChunk(t, server, base, started.ID, 0, []byte("he"), 64<<20+1); huge.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over 64 MiB = %d", huge.StatusCode)
	}
	if past := patchChunk(t, server, base, started.ID, 0, []byte("hello!"), 6); past.StatusCode != http.StatusBadRequest {
		t.Fatalf("past end = %d", past.StatusCode)
	}
	if early, _ := completeUpload(t, server, base, started.ID); early != http.StatusConflict {
		t.Fatalf("complete before the last byte = %d", early)
	}
}

func TestUploadCompleteMapsRefusal(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = plentyFree
	base := uploadsBase(fixture)
	started := mustStartUpload(t, server, base, "x.bin", 5)
	patchChunk(t, server, base, started.ID, 0, []byte("hello"), 5)

	server.volumeRunner.SetErr(&volumes.RefusalError{Message: "need 5 bytes, 1 available"})
	status, view := completeUpload(t, server, base, started.ID)
	if status != http.StatusOK || view.State != "failed" || view.ErrorKind != "refused" || !strings.Contains(view.Error, "need 5 bytes") {
		t.Fatalf("refused complete = %d %+v", status, view)
	}

	server.volumeRunner.SetErr(nil)
	status, view = completeUpload(t, server, base, started.ID)
	if status != http.StatusOK || view.State != "done" {
		t.Fatalf("retry = %d %+v", status, view)
	}
}

// A role lost after the last chunk still stops the write: complete re-runs
// the start checks under the caller's current rights.
func TestUploadCompleteRechecksAccess(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = plentyFree
	signInAsUser(t, server, 2, "editor@example.com", false)
	if _, err := server.db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, ?, NULL, 'editor')", fixture.project.ID); err != nil {
		t.Fatal(err)
	}
	base := uploadsBase(fixture)
	started := mustStartUpload(t, server, base, "x.bin", 5)
	patchChunk(t, server, base, started.ID, 0, []byte("hello"), 5)

	if _, err := server.db.Exec("UPDATE grants SET role = 'viewer' WHERE user_id = 2"); err != nil {
		t.Fatal(err)
	}
	if status, _ := completeUpload(t, server, base, started.ID); status != http.StatusForbidden {
		t.Fatalf("complete as viewer = %d, want 403", status)
	}
	if writes := writeCalls(server.volumeRunner.Snapshot()); len(writes) != 0 {
		t.Fatalf("writes after losing the role = %+v", writes)
	}
}

func TestUploadsAreUserAndComposeScoped(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = plentyFree
	base := uploadsBase(fixture)
	started := mustStartUpload(t, server, base, "x.bin", 5)
	patchChunk(t, server, base, started.ID, 0, []byte("hello"), 5)
	finished := mustStartUpload(t, server, base, "done.bin", 1)
	patchChunk(t, server, base, finished.ID, 0, []byte("x"), 1)
	if status, _ := completeUpload(t, server, base, finished.ID); status != http.StatusOK {
		t.Fatalf("complete = %d", status)
	}

	other := createCompose(t, server, fixture.project.ID, fixture.production.ID, "other")
	otherBase := composeVolumesPath(fixture.project.ID, fixture.production.ID, other.ID) + "/uploads"
	if response := server.request(t, http.MethodGet, otherBase+"/"+started.ID, nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("GET under another Compose = %d", response.StatusCode)
	}
	if response := patchChunk(t, server, otherBase, started.ID, 5, nil, 0); response.StatusCode != http.StatusNotFound {
		t.Errorf("PATCH under another Compose = %d", response.StatusCode)
	}
	if status, _ := completeUpload(t, server, otherBase, started.ID); status != http.StatusNotFound {
		t.Errorf("complete under another Compose = %d", status)
	}
	if response := server.request(t, http.MethodDelete, otherBase+"/"+started.ID, nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE under another Compose = %d", response.StatusCode)
	}
	if writes := writeCalls(server.volumeRunner.Snapshot()); len(writes) != 1 {
		t.Fatalf("writes = %+v, want only done.bin's", writes)
	}

	var list struct {
		Uploads []uploadView `json:"uploads"`
	}
	decodeJSON(t, server.request(t, http.MethodGet, base, nil), &list)
	if len(list.Uploads) != 1 || list.Uploads[0].ID != started.ID {
		t.Fatalf("list = %+v, want only the open upload", list.Uploads)
	}

	signInAsUser(t, server, 2, "second@example.com", true)
	if response := server.request(t, http.MethodGet, base+"/"+started.ID, nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("another User's GET = %d", response.StatusCode)
	}
	decodeJSON(t, server.request(t, http.MethodGet, base, nil), &list)
	if len(list.Uploads) != 0 {
		t.Errorf("another User lists %+v", list.Uploads)
	}
}

func TestUploadCancel(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	server.volumeRunner.Stdout = plentyFree
	base := uploadsBase(fixture)
	started := mustStartUpload(t, server, base, "x.bin", 5)
	if response := server.request(t, http.MethodDelete, base+"/"+started.ID, nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel = %d", response.StatusCode)
	}
	if response := server.request(t, http.MethodGet, base+"/"+started.ID, nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("after cancel = %d", response.StatusCode)
	}
}

// gatedRunner answers free with plenty of space, records writes, and keeps an
// archive download open until release: a Volume read holding the Project's
// shared volume lock.
type gatedRunner struct {
	mu      sync.Mutex
	writes  []string
	started chan struct{}
	release chan struct{}
}

func (r *gatedRunner) Available(context.Context) error { return nil }
func (r *gatedRunner) Run(ctx context.Context, req volumes.Request, stdin io.Reader, stdout io.Writer) error {
	switch req.Op {
	case volumes.OpFree:
		_, err := io.WriteString(stdout, plentyFree)
		return err
	case volumes.OpArchive:
		stdout.Write([]byte("partial-tar"))
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	case volumes.OpWrite:
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		r.mu.Lock()
		r.writes = append(r.writes, string(b))
		r.mu.Unlock()
	}
	return nil
}

func (r *gatedRunner) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.writes)
}

// Chunks never wait for the Project's locks; the write into the Volume does,
// in the background, and finishes once they are free.
func TestUploadRoutesSkipTheProjectLocks(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	fixture := setupExplorerCompose(t, server)
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	server.stopWorker()
	server.handler, server.stopWorker = startHandler(t, server.db, newHoldRunner(), server.dataDir, nil, nil, runner, volumes.NewResolver(server.volumeCLI, server.dataDir), nil, nil)
	released := false
	t.Cleanup(func() {
		if !released {
			close(runner.release)
		}
	})

	q := url.Values{"source": {"demoapp_pgdata"}, "service": {"db"}, "rootConfirmed": {"1"}, "bindConfirmed": {"1"}}
	archive := httptest.NewRequest(http.MethodGet, "http://relist-panel.test"+composeVolumesPath(fixture.project.ID, fixture.production.ID, fixture.compose.ID)+"/archive?"+q.Encode(), nil)
	for _, c := range server.cookies {
		archive.AddCookie(c)
	}
	go server.handler.ServeHTTP(httptest.NewRecorder(), archive)
	waitSignal(t, runner.started, "archive stream")

	base := uploadsBase(fixture)
	started := mustStartUpload(t, server, base, "x.bin", 5)
	if response := patchChunk(t, server, base, started.ID, 0, []byte("hello"), 5); response.StatusCode != http.StatusNoContent {
		t.Fatalf("chunk while a Volume read is open = %d", response.StatusCode)
	}
	status, view := completeUpload(t, server, base, started.ID)
	if status != http.StatusAccepted || view.State != "consuming" {
		t.Fatalf("complete while the volume lock is shared = %d %+v", status, view)
	}
	if runner.writeCount() != 0 {
		t.Fatal("write ran while a Volume read held the lock")
	}

	close(runner.release)
	released = true
	eventually(t, "the upload to finish", func() bool {
		var got uploadView
		decodeJSON(t, server.request(t, http.MethodGet, base+"/"+started.ID, nil), &got)
		return got.State == "done"
	})
	if runner.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1", runner.writeCount())
	}
}

