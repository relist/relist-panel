package dockerexec_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/dockerhost"
)

func TestDockerHostReportPreservesUnknownVolumeUsageAndHidesDaemonSecrets(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"Version":"28.0","ApiVersion":"1.48"}`))
		case "/info":
			_, _ = w.Write([]byte(`{"Name":"host","Driver":"overlay2","DockerRootDir":"/var/lib/docker","ContainersRunning":1,"ContainersStopped":1,"ContainersPaused":0,"RegistryConfig":{"password":"do-not-expose"}}`))
		case "/system/df":
			_, _ = w.Write([]byte(`{"LayersSize":500,"Images":[{"Id":"a","Containers":1},{"Id":"b","Containers":0}],"Containers":[{"Id":"c","Names":["/web"],"Image":"web:1","State":"running","SizeRw":40},{"Id":"d","Names":["/old"],"Image":"old:1","State":"exited","SizeRw":10}],"Volumes":[{"Name":"data","Driver":"local","UsageData":{"Size":100,"RefCount":1}},{"Name":"old","Driver":"local","UsageData":{"Size":20,"RefCount":0}},{"Name":"negative","UsageData":{"Size":-1,"RefCount":-1}}],"BuildCache":[{"Size":30,"InUse":false,"Shared":false},{"Size":10,"InUse":true,"Shared":true}]}`))
		case "/volumes":
			_, _ = w.Write([]byte(`{"Volumes":[{"Name":"data","Driver":"local"},{"Name":"old","Driver":"local"},{"Name":"unknown","Driver":"plugin"},{"Name":"negative","Driver":"local"}]}`))
		case "/networks":
			_, _ = w.Write([]byte(`[{"Id":"n","Name":"default","Driver":"bridge","Scope":"local","Containers":{"c":{}}}]`))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
		}
	}))
	report, err := dockerexec.NewDockerHost(socket).Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != "28.0" || report.ContainersRunning != 1 || len(report.Usage) != 5 {
		t.Fatalf("report: %+v", report)
	}
	for _, u := range report.Usage {
		switch u.Kind {
		case "images":
			if u.Size == nil || *u.Size != 500 || u.Active != 1 || u.Reclaimable != nil {
				t.Fatalf("images: %+v", u)
			}
		case "containers":
			if *u.Size != 50 || *u.Reclaimable != 10 {
				t.Fatalf("containers: %+v", u)
			}
		case "volumes":
			if u.Size != nil || u.Reclaimable != nil {
				t.Fatalf("unknown volumes became a number: %+v", u)
			}
		case "build-cache":
			if *u.Size != 40 || *u.Reclaimable != 30 {
				t.Fatalf("cache: %+v", u)
			}
		}
	}
	if len(report.Volumes) != 4 {
		t.Fatalf("missing non-df volumes: %+v", report.Volumes)
	}
	if report.Volumes[2].Size != nil || report.Volumes[2].References != nil || report.Volumes[3].Size != nil {
		t.Fatalf("volume unavailable: %+v", report.Volumes)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "do-not-expose") {
		t.Fatal("daemon info leaked")
	}
}

func TestDockerHostPruneUsesExplicitFiltersAndNeverForceDeletes(t *testing.T) {
	for _, tt := range []struct {
		kind               dockerhost.Kind
		path, filters, all string
	}{
		{dockerhost.Containers, "containers", "", ""},
		{dockerhost.Networks, "networks", "", ""},
		{dockerhost.BuildCache, "build", "", "true"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/version" {
					_, _ = w.Write([]byte(`{"ApiVersion":"1.48"}`))
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1.48/"+tt.path+"/prune" || r.URL.Query().Get("filters") != tt.filters || r.URL.Query().Get("all") != tt.all {
					t.Errorf("unexpected prune: %s %s", r.Method, r.URL.String())
				}
				_, _ = w.Write([]byte(`{"SpaceReclaimed":12,"ImagesDeleted":[{"Deleted":"i","Untagged":"web:1"}],"ContainersDeleted":["c"],"NetworksDeleted":["n"],"VolumesDeleted":["v"],"CachesDeleted":["b"]}`))
			}))
			got, err := dockerexec.NewDockerHost(socket).Prune(context.Background(), tt.kind)
			if err != nil || got.SpaceReclaimed != 12 || len(got.Deleted) == 0 {
				t.Fatalf("prune: %+v %v", got, err)
			}
		})
	}
}
func TestDockerHostPruneRefusesKindsThatMustBePlanned(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a planned kind reached Docker's prune: %s", r.URL)
	}))
	for _, kind := range []dockerhost.Kind{dockerhost.Images, dockerhost.UnusedImages, dockerhost.Volumes, dockerhost.AllVolumes} {
		if _, err := dockerexec.NewDockerHost(socket).Prune(context.Background(), kind); err == nil {
			t.Fatalf("host-wide prune of %s was permitted", kind)
		}
	}
}

func TestDockerHostVolumeCandidates(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/volumes":
			if r.URL.Query().Get("filters") != `{"dangling":["true"]}` {
				t.Errorf("filters = %q", r.URL.Query().Get("filters"))
			}
			_, _ = w.Write([]byte(`{"Volumes":[{"Name":"anon","Labels":{"com.docker.volume.anonymous":""}},{"Name":"named","Labels":{"com.docker.compose.project":"p"}},{"Name":"bare","Labels":null}]}`))
		case "/system/df":
			_, _ = w.Write([]byte(`{"Volumes":[{"Name":"named","UsageData":{"Size":55,"RefCount":0}},{"Name":"bare","UsageData":{"Size":-1,"RefCount":-1}}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	host := dockerexec.NewDockerHost(socket)
	got, err := host.Candidates(context.Background(), dockerhost.Volumes)
	if err != nil || len(got) != 1 || got[0].Name != "anon" {
		t.Fatalf("volumes: %+v %v", got, err)
	}
	got, err = host.Candidates(context.Background(), dockerhost.AllVolumes)
	if err != nil || len(got) != 3 || got[1].Labels["com.docker.compose.project"] != "p" || got[1].Size == nil || *got[1].Size != 55 || got[2].Size != nil {
		t.Fatalf("all volumes: %+v %v", got, err)
	}
}

func TestDockerHostImageCandidates(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/json":
			_, _ = w.Write([]byte(`[{"Id":"sha256:used","RepoTags":["web:1"],"Size":10},{"Id":"sha256:dangling","RepoTags":null,"Size":20},{"Id":"sha256:none","RepoTags":["<none>:<none>"],"Size":5},{"Id":"sha256:tagged","RepoTags":["old:1","old:2"],"Size":30,"Labels":{"com.docker.compose.project":"p"}}]`))
		case "/containers/json":
			if r.URL.Query().Get("all") != "1" {
				t.Errorf("containers query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"Id":"c","ImageID":"sha256:used"}]`))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	host := dockerexec.NewDockerHost(socket)
	got, err := host.Candidates(context.Background(), dockerhost.Images)
	if err != nil || len(got) != 2 || got[0].ID != "sha256:dangling" || got[0].Name != "sha256:dangling" || got[1].ID != "sha256:none" || len(got[1].Tags) != 0 {
		t.Fatalf("dangling: %+v %v", got, err)
	}
	got, err = host.Candidates(context.Background(), dockerhost.UnusedImages)
	if err != nil || len(got) != 3 || got[2].Name != "old:1" || len(got[2].Tags) != 2 || got[2].Labels["com.docker.compose.project"] != "p" || *got[2].Size != 30 {
		t.Fatalf("unused: %+v %v", got, err)
	}
}

func TestDockerHostRemoveIsUnforcedAndMapsConflictToRefusal(t *testing.T) {
	var requests []string
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" {
			t.Errorf("forced delete: %s", r.URL)
		}
		switch r.URL.Path {
		case "/volumes/busy", "/images/sha256:busy":
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"message":"remove busy: volume is in use"}`))
		case "/volumes/broken":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"message":"disk on fire"}`))
		default:
			w.WriteHeader(204)
		}
	}))
	host := dockerexec.NewDockerHost(socket)
	ctx := context.Background()
	if removed, refusal, err := host.Remove(ctx, dockerhost.AllVolumes, dockerhost.Candidate{Name: "gone"}); !removed || refusal != "" || err != nil {
		t.Fatalf("volume: %v %q %v", removed, refusal, err)
	}
	if removed, refusal, err := host.Remove(ctx, dockerhost.AllVolumes, dockerhost.Candidate{Name: "busy"}); removed || !strings.Contains(refusal, "in use") || err != nil {
		t.Fatalf("busy volume: %v %q %v", removed, refusal, err)
	}
	if _, _, err := host.Remove(ctx, dockerhost.AllVolumes, dockerhost.Candidate{Name: "broken"}); err == nil {
		t.Fatal("a 500 was treated as a refusal")
	}
	if removed, _, err := host.Remove(ctx, dockerhost.UnusedImages, dockerhost.Candidate{ID: "sha256:img", Name: "sha256:img"}); !removed || err != nil {
		t.Fatalf("image: %v %v", removed, err)
	}
	if removed, refusal, err := host.Remove(ctx, dockerhost.UnusedImages, dockerhost.Candidate{ID: "sha256:busy", Name: "x"}); removed || refusal == "" || err != nil {
		t.Fatalf("busy image: %v %q %v", removed, refusal, err)
	}
	if removed, _, err := host.Remove(ctx, dockerhost.UnusedImages, dockerhost.Candidate{ID: "sha256:multi", Name: "a:1", Tags: []string{"reg:5000/a:1", "a:2"}}); !removed || err != nil {
		t.Fatalf("multi-tag image: %v %v", removed, err)
	}
	want := []string{"DELETE /volumes/gone", "DELETE /volumes/busy", "DELETE /volumes/broken", "DELETE /images/sha256:img", "DELETE /images/sha256:busy", "DELETE /images/reg:5000/a:1", "DELETE /images/a:2"}
	if strings.Join(requests, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %v", requests)
	}
}

func TestDockerHostLostPruneResultIsUncertain(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"ApiVersion":"1.48"}`))
			return
		}
		_, _ = w.Write([]byte(`{"SpaceReclaimed":12`))
	}))
	_, err := dockerexec.NewDockerHost(socket).Prune(context.Background(), dockerhost.Containers)
	if !errors.Is(err, dockerhost.ErrPruneUncertain) {
		t.Fatalf("lost result was treated as finished: %v", err)
	}
}
