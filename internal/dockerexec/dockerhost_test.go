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
		{dockerhost.Images, "images", `{"dangling":["true"]}`, ""},
		{dockerhost.UnusedImages, "images", `{"dangling":["false"]}`, ""},
		{dockerhost.Volumes, "volumes", `{"all":["false"]}`, ""},
		{dockerhost.AllVolumes, "volumes", `{"all":["true"]}`, ""},
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
func TestDockerHostRefusesOldAPIVolumePrune(t *testing.T) {
	socket := newTerminalDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Error("old daemon received deletion")
		}
		_, _ = w.Write([]byte(`{"ApiVersion":"1.41"}`))
	}))
	if _, err := dockerexec.NewDockerHost(socket).Prune(context.Background(), dockerhost.Volumes); err == nil {
		t.Fatal("old volume prune was permitted")
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
