package builders

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"relist-panel/internal/authz"
	"relist-panel/internal/grants"
)

func (f *serviceFixture) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	f.svc.RegisterRoutes(mux)
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, reader))
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: body %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

func TestListBuilders(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	if err := f.store.SetComposeChoice(ctx, f.compose(t, "web"), Choice{Mode: ModeBuilder, BuilderID: b.ID}); err != nil {
		t.Fatal(err)
	}
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	code, body := f.do(t, "GET", "/api/builders", "")
	if code != 200 {
		t.Fatalf("code = %d, body %v", code, body)
	}
	raw, _ := json.Marshal(body)
	want := `{"builders":[{"cpus":2,"id":1,"lastError":"","maxParallelism":4,"memoryMb":4096,"name":"large","state":"running","usedBy":["web"]}],"buildkitImage":"","capacity":{"cpus":16,"memoryMb":31900},"defaultBuilderId":0,"defaultBuildkitImage":"` + DefaultBuildKitImage + `"}`
	if string(raw) != want {
		t.Fatalf("body = %s\nwant   %s", raw, want)
	}
}

func TestCreateUpdateDeleteRoutes(t *testing.T) {
	f := newServiceFixture(t)
	code, body := f.do(t, "POST", "/api/builders", `{"name":"small","cpus":0.5,"memoryMb":1024,"maxParallelism":null}`)
	if code != 201 || body["name"] != "small" || body["cpus"] != 0.5 || body["maxParallelism"] != nil {
		t.Fatalf("POST = %d %v", code, body)
	}
	id := strconv.Itoa(int(body["id"].(float64)))
	code, body = f.do(t, "PUT", "/api/builders/"+id, `{"name":"small","cpus":1,"memoryMb":2048,"maxParallelism":2}`)
	if code != 200 || body["memoryMb"] != float64(2048) || body["maxParallelism"] != float64(2) {
		t.Fatalf("PUT = %d %v", code, body)
	}
	if code, body = f.do(t, "DELETE", "/api/builders/"+id, ""); code != 204 {
		t.Fatalf("DELETE = %d %v", code, body)
	}
	if code, _ = f.do(t, "PUT", "/api/builders/"+id, `{"name":"x","cpus":1,"memoryMb":2048}`); code != 404 {
		t.Fatalf("PUT missing = %d", code)
	}
}

func TestValidationIs422WithField(t *testing.T) {
	f := newServiceFixture(t)
	for body, field := range map[string]string{
		`{"name":"","cpus":1,"memoryMb":1024}`:                      "name",
		`{"name":"a","cpus":0.15,"memoryMb":1024}`:                  "cpus",
		`{"name":"a","cpus":1,"memoryMb":100}`:                      "memoryMb",
		`{"name":"a","cpus":1,"memoryMb":1024,"maxParallelism":99}`: "maxParallelism",
	} {
		code, out := f.do(t, "POST", "/api/builders", body)
		if code != 422 || out["field"] != field {
			t.Errorf("POST %s = %d %v, want 422 on %s", body, code, out, field)
		}
	}
}

func TestDeleteConflictsAre409(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	_ = f.store.SetDefault(ctx, b.ID)
	code, out := f.do(t, "DELETE", "/api/builders/1", "")
	if code != 409 || out["error"] != ErrIsDefault.Error() {
		t.Fatalf("DELETE default = %d %v", code, out)
	}
	_ = f.store.SetDefault(ctx, DockerDefault)
	_ = f.store.SetComposeChoice(ctx, f.compose(t, "web"), Choice{Mode: ModeBuilder, BuilderID: b.ID})
	code, out = f.do(t, "DELETE", "/api/builders/1", "")
	if code != 409 || out["error"] != "These Composes use this Builder: web" {
		t.Fatalf("DELETE used = %d %v", code, out)
	}
}

func TestDefaultAndSettingsRoutes(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	if code, out := f.do(t, "PUT", "/api/builders/default", `{"builderId":1}`); code != 204 {
		t.Fatalf("PUT default = %d %v", code, out)
	}
	if got, _ := f.store.DefaultID(ctx); got != b.ID {
		t.Fatalf("default = %d", got)
	}
	if code, _ := f.do(t, "PUT", "/api/builders/default", `{"builderId":9}`); code != 422 {
		t.Fatalf("PUT unknown default = %d", code)
	}
	code, out := f.do(t, "PUT", "/api/builders/settings", `{"buildkitImage":"moby/buildkit:latest"}`)
	if code != 422 || out["error"] != ErrInvalidImage.Error() {
		t.Fatalf("PUT invalid image = %d %v", code, out)
	}
	f.engine.pullErr = errors.New("manifest unknown")
	code, out = f.do(t, "PUT", "/api/builders/settings", `{"buildkitImage":"`+newRef+`"}`)
	if code != 502 || out["error"] != "Could not pull "+newRef+": manifest unknown" {
		t.Fatalf("PUT unpullable image = %d %v", code, out)
	}
	f.engine.pullErr = nil
	if code, out = f.do(t, "PUT", "/api/builders/settings", `{"buildkitImage":"`+newRef+`"}`); code != 204 {
		t.Fatalf("PUT image = %d %v", code, out)
	}
}

func TestStartDiskUsagePruneRoutes(t *testing.T) {
	f := newServiceFixture(t)
	f.builder(t, "large")
	f.engine.diskUsage = 4172000000
	if code, out := f.do(t, "GET", "/api/builders/1/disk-usage", ""); code != 200 || out["bytes"] != float64(4172000000) {
		t.Fatalf("disk-usage = %d %v", code, out)
	}
	if code, out := f.do(t, "POST", "/api/builders/1/prune", ""); code != 204 {
		t.Fatalf("prune = %d %v", code, out)
	}
	if code, out := f.do(t, "POST", "/api/builders/1/start", ""); code != 200 || out["name"] != "large" {
		t.Fatalf("start = %d %v", code, out)
	}
	f.engine.ensureErr = errors.New("boom")
	if code, out := f.do(t, "POST", "/api/builders/1/start", ""); code != 502 || out["error"] != "Could not start Builder large: boom" {
		t.Fatalf("failed start = %d %v", code, out)
	}
	if code, _ := f.do(t, "POST", "/api/builders/7/prune", ""); code != 404 {
		t.Fatalf("prune missing = %d", code)
	}
}

func TestPruneWhileBuildingIs409(t *testing.T) {
	f := newServiceFixture(t)
	f.builder(t, "large")
	_, release, err := f.svc.Acquire(context.Background(), 1, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if code, out := f.do(t, "POST", "/api/builders/1/prune", ""); code != 409 || out["error"] != ErrBusy.Error() {
		t.Fatalf("prune while busy = %d %v", code, out)
	}
}

func TestDockerRefusalsAre502WithDockersWords(t *testing.T) {
	f := newServiceFixture(t)
	f.builder(t, "large")
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	f.engine.ensureErr = errors.New("Cannot update container: memory is below usage")
	code, out := f.do(t, "PUT", "/api/builders/1", `{"name":"large","cpus":2,"memoryMb":1024,"maxParallelism":4}`)
	if code != 502 || out["error"] != "Docker refused: Cannot update container: memory is below usage" {
		t.Fatalf("PUT refused = %d %v", code, out)
	}
	f.engine.pruneErr = errors.New("prune failed")
	if code, out := f.do(t, "POST", "/api/builders/1/prune", ""); code != 502 || out["error"] != "Docker refused: prune failed" {
		t.Fatalf("prune refused = %d %v", code, out)
	}
	f.engine.removeErr = errors.New("remove failed")
	if code, out := f.do(t, "DELETE", "/api/builders/1", ""); code != 502 || out["error"] != "Docker refused: remove failed" {
		t.Fatalf("delete refused = %d %v", code, out)
	}
}

func TestListBuildersForAMemberNamesThemOnly(t *testing.T) {
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	if err := f.store.SetDefault(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	f.engine.capacityErr = errors.New("a member's list must not ask Docker")
	mux := http.NewServeMux()
	f.svc.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/builders", nil)
	request = request.WithContext(authz.WithAccess(request.Context(), grants.Access{}))
	mux.ServeHTTP(rec, request)
	want := `{"builders":[{"id":1,"name":"large"}],"defaultBuilderId":1}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("member list = %d %s\nwant %s", rec.Code, rec.Body.String(), want)
	}
}
