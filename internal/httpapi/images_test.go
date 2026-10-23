package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/images"
)

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

func errorOf(t *testing.T, response *http.Response) string {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(readBody(t, response)), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return payload.Error
}

func TestImagesListSortsNewestFirstWithTagsNeverNull(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	server.images.Listed = []images.Image{
		{ID: "sha256:a", Created: t0},
		{ID: "sha256:b", Tags: []string{"web:1"}, Created: t0.Add(time.Hour), InUse: true},
	}
	response := server.request(t, http.MethodGet, "/api/images", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	raw := readBody(t, response)
	if !strings.Contains(raw, `"tags":[]`) {
		t.Fatalf("body %s lacks \"tags\":[]", raw)
	}
	var payload struct {
		Images []struct {
			ID    string   `json:"id"`
			Tags  []string `json:"tags"`
			InUse bool     `json:"inUse"`
		} `json:"images"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Images) != 2 || payload.Images[0].ID != "sha256:b" || payload.Images[1].ID != "sha256:a" {
		t.Fatalf("order = %+v, want b then a", payload.Images)
	}
	if !payload.Images[0].InUse {
		t.Fatalf("b.inUse = false, want true")
	}
}

func TestImagesListTiesBreakByID(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	server.images.Listed = []images.Image{{ID: "sha256:b", Created: t0}, {ID: "sha256:a", Created: t0}}
	raw := readBody(t, server.request(t, http.MethodGet, "/api/images", nil))
	if strings.Index(raw, "sha256:a") > strings.Index(raw, "sha256:b") {
		t.Fatalf("tie not broken by id: %s", raw)
	}
}

func TestImagesListUnavailableAnswers503(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.ListErr = images.ErrUnavailable
	response := server.request(t, http.MethodGet, "/api/images", nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	if got := errorOf(t, response); got != "Docker is not reachable" {
		t.Fatalf("error = %q", got)
	}
}

func TestImagesUploadRequiresContentLength(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	response := sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), -1)
	if response.StatusCode != http.StatusLengthRequired {
		t.Fatalf("status = %d, want 411", response.StatusCode)
	}
	if got := errorOf(t, response); got != "upload requires a declared length (Content-Length)" {
		t.Fatalf("error = %q", got)
	}
	if n := len(server.images.LoadedArchives()); n != 0 {
		t.Fatalf("Load called %d times", n)
	}
}

func TestImagesUploadOverCapAnswers413BeforeDocker(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	response := sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), images.UploadCap+1)
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.StatusCode)
	}
	if got := errorOf(t, response); got != "upload exceeds the 10 GiB cap" {
		t.Fatalf("error = %q", got)
	}
	if n := len(server.images.LoadedArchives()); n != 0 {
		t.Fatalf("Load called %d times", n)
	}

	// Exactly at the cap is accepted (the tiny real body is cut short, which
	// is not a 413).
	response = sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), images.UploadCap)
	defer response.Body.Close()
	if response.StatusCode == http.StatusRequestEntityTooLarge {
		t.Fatalf("status at exactly the cap = 413")
	}
}

func TestImagesUploadStreamsBytesAndReturnsNames(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.Names = []string{"web:1", "sha256:abc"}
	body := []byte("archive-bytes")
	response := sendUpload(t, server, http.MethodPut, "/api/images", body, int64(len(body)))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var payload struct {
		Images []string `json:"images"`
	}
	if err := json.Unmarshal([]byte(readBody(t, response)), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fmt.Sprint(payload.Images) != "[web:1 sha256:abc]" {
		t.Fatalf("images = %v", payload.Images)
	}
	loaded := server.images.LoadedArchives()
	if len(loaded) != 1 || string(loaded[0]) != string(body) {
		t.Fatalf("loaded = %q, want %q", loaded, body)
	}
}

func TestImagesUploadLoadFailedAnswers422WithDaemonMessage(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.LoadErr = fmt.Errorf("%w: %s", images.ErrLoadFailed, "invalid tar header")
	response := sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), 1)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	if got := errorOf(t, response); got != "invalid tar header" {
		t.Fatalf("error = %q", got)
	}
}

func TestImagesUploadUnavailableAnswers503(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.LoadErr = images.ErrUnavailable
	response := sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), 1)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	if got := errorOf(t, response); got != "Docker is not reachable" {
		t.Fatalf("error = %q", got)
	}
}

func TestImagesUploadOtherErrorAnswers500(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.LoadErr = io.ErrUnexpectedEOF
	response := sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), 1)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.StatusCode)
	}
	if got := errorOf(t, response); got == "" {
		t.Fatalf("error is empty")
	}
}

func TestImagesRoutesRequireAuth(t *testing.T) {
	server := newTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		response := sendUpload(t, server, method, "/api/images", []byte("x"), 1)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", method, response.StatusCode)
		}
	}
}

func TestImagesRoutesAbsentWithoutStore(t *testing.T) {
	server := newTestServer(t)
	server.useRunner(t, server.runner)
	setupAndLogin(t, server)
	response := server.request(t, http.MethodGet, "/api/images", nil)
	defer response.Body.Close()
	if ct := response.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want non-JSON", ct)
	}
}

func TestImagesUploadWithNoNamesAnswers422(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	response := sendUpload(t, server, http.MethodPut, "/api/images", []byte("x"), 1)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	if got := errorOf(t, response); got != "archive contained no images" {
		t.Fatalf("error = %q", got)
	}
}

// shortBody yields some bytes and then fails, like a client that disconnects
// before its declared Content-Length.
type shortBody struct{ sent bool }

func (b *shortBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, "partial"), nil
}

func TestImagesUploadShorterThanDeclaredLengthNeverAnswers200(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	request := httptest.NewRequest(http.MethodPut, "http://relist-panel.test/api/images", &shortBody{})
	request.ContentLength = 1000
	response := server.serve(request)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.StatusCode)
	}
	if got := errorOf(t, response); got == "" {
		t.Fatalf("error is empty")
	}
}

// cancelOnRead cancels its request context on the first read, then fails.
type cancelOnRead struct{ cancel context.CancelFunc }

func (c *cancelOnRead) Read([]byte) (int, error) {
	c.cancel()
	return 0, io.ErrUnexpectedEOF
}

func TestImagesUploadClientCancelWritesNoErrorBody(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.LoadErr = context.Canceled
	// The context is cancelled while Load reads the body (after auth has
	// passed), as when the browser disconnects mid-upload.
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPut, "http://relist-panel.test/api/images", &cancelOnRead{cancel: cancel}).WithContext(ctx)
	request.ContentLength = 1
	response := server.serve(request)
	// Asserted: the handler writes no 500 and no JSON error body (the
	// recorder keeps its untouched 200 default and an empty body).
	if response.StatusCode == http.StatusInternalServerError {
		t.Fatalf("status = 500 after client cancel")
	}
	if raw := readBody(t, response); raw != "" {
		t.Fatalf("body = %q, want empty", raw)
	}
}

// countingBody counts the bytes read from it.
type countingBody struct {
	r io.Reader
	n int
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestImagesUploadLoadFailedDrainsTheBodyBefore422(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	server.images.LoadErr = fmt.Errorf("%w: %s", images.ErrLoadFailed, "invalid tar header")
	server.images.ReadLimit = 4
	payload := bytes.Repeat([]byte("z"), 100000)
	body := &countingBody{r: bytes.NewReader(payload)}
	request := httptest.NewRequest(http.MethodPut, "http://relist-panel.test/api/images", body)
	request.ContentLength = int64(len(payload))
	response := server.serve(request)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	if got := errorOf(t, response); got != "invalid tar header" {
		t.Fatalf("error = %q", got)
	}
	if body.n != len(payload) {
		t.Fatalf("consumed %d of %d body bytes", body.n, len(payload))
	}
}
