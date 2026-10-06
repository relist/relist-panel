package dockerexec_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/images"
)

func TestImagesListJoinsContainersForInUse(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/json":
			writeTerminalJSON(t, w, 200, []map[string]any{
				{"Id": "sha256:aaa", "RepoTags": []string{"web:1", "web:latest"}, "Size": 100, "Created": 1700000000},
				{"Id": "sha256:bbb", "RepoTags": []string{"<none>:<none>"}, "Size": 5, "Created": 1700000001},
			})
		case "/containers/json":
			if got := r.URL.Query().Get("all"); got != "1" {
				t.Errorf("containers query all = %q, want 1", got)
			}
			writeTerminalJSON(t, w, 200, []map[string]any{{"Id": "c1", "ImageID": "sha256:aaa"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))

	got, err := dockerexec.NewImages(socket).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d images, want 2", len(got))
	}
	first := got[0]
	if strings.Join(first.Tags, ",") != "web:1,web:latest" || !first.InUse || first.Size != 100 ||
		!first.Created.Equal(time.Unix(1700000000, 0)) || first.ID != "sha256:aaa" {
		t.Errorf("first = %+v", first)
	}
	second := got[1]
	if second.Tags == nil || len(second.Tags) != 0 || second.InUse {
		t.Errorf("second = %+v, want empty non-nil Tags and not in use", second)
	}
}

func TestImagesListNullRepoTagsIsEmpty(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/json" {
			_, _ = w.Write([]byte(`[{"Id":"x","RepoTags":null,"Size":1,"Created":1}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	got, err := dockerexec.NewImages(socket).List(context.Background())
	if err != nil || len(got) != 1 || got[0].Tags == nil || len(got[0].Tags) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestImagesLoadPostsTheBodyAndParsesNames(t *testing.T) {
	archive := []byte("tar-bytes-here")
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/images/load" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("quiet") != "1" {
			t.Errorf("query = %q, want quiet=1", r.URL.RawQuery)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-tar" {
			t.Errorf("Content-Type = %q", ct)
		}
		if r.ContentLength != int64(len(archive)) {
			t.Errorf("ContentLength = %d, want %d", r.ContentLength, len(archive))
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, archive) {
			t.Errorf("body = %q", body)
		}
		_, _ = w.Write([]byte(`{"stream":"Loaded image: web:1\n"}` + "\n" + `{"stream":"Loaded image ID: sha256:abc\n"}` + "\n"))
	}))

	names, err := dockerexec.NewImages(socket).Load(context.Background(), bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Join(names, ",") != "web:1,sha256:abc" {
		t.Errorf("names = %v", names)
	}
}

func loadAgainst(t *testing.T, reply string, status int) ([]string, error) {
	t.Helper()
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	return dockerexec.NewImages(socket).Load(context.Background(), strings.NewReader("x"), 1)
}

func TestImagesLoadErrorAfterNamesFails(t *testing.T) {
	names, err := loadAgainst(t, `{"stream":"Loaded image: web:1\n"}`+"\n"+
		`{"errorDetail":{"message":"unexpected EOF"},"error":"unexpected EOF"}`+"\n", 200)
	if !errors.Is(err, images.ErrLoadFailed) || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("err = %v", err)
	}
	if names != nil {
		t.Errorf("names = %v, want none", names)
	}
}

func TestImagesLoadEmptyStreamFails(t *testing.T) {
	_, err := loadAgainst(t, "", 200)
	if !errors.Is(err, images.ErrLoadFailed) || !strings.Contains(err.Error(), "archive contained no images") {
		t.Fatalf("err = %v", err)
	}
}

func TestImagesLoadReadsLongLines(t *testing.T) {
	long := strings.Repeat("x", 100*1024)
	_, err := loadAgainst(t, `{"errorDetail":{"message":"`+long+`"}}`+"\n", 200)
	if !errors.Is(err, images.ErrLoadFailed) || !strings.Contains(err.Error(), long) {
		t.Fatalf("err = %.200v", err)
	}
}

func TestImagesLoadNon2xxFails(t *testing.T) {
	_, err := loadAgainst(t, `{"message":"boom"}`, 500)
	if !errors.Is(err, images.ErrLoadFailed) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestImagesUnreachableSocketIsUnavailable(t *testing.T) {
	adapter := dockerexec.NewImages(filepath.Join(t.TempDir(), "missing.sock"))
	if _, err := adapter.List(context.Background()); !errors.Is(err, images.ErrUnavailable) {
		t.Errorf("List err = %v", err)
	}
	if _, err := adapter.Load(context.Background(), strings.NewReader("x"), 1); !errors.Is(err, images.ErrUnavailable) {
		t.Errorf("Load err = %v", err)
	}
}

func TestImagesLoadStopsWhenTheContextIsCancelled(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() {
		_, err := dockerexec.NewImages(socket).Load(ctx, pr, int64(1<<30))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Load returned nil after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Load did not return within 2s of cancel")
	}
}
