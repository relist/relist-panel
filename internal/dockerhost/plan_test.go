package dockerhost

import (
	"reflect"
	"testing"
)

func names(entries []PlanEntry) []string {
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestNormalizeReference(t *testing.T) {
	for in, want := range map[string]string{
		"nginx":                              "nginx:latest",
		"nginx:1.27":                         "nginx:1.27",
		"docker.io/library/nginx":            "nginx:latest",
		"index.docker.io/library/nginx":      "nginx:latest",
		"docker.io/library/nginx:latest":     "nginx:latest",
		"library/nginx:1":                    "nginx:1",
		"docker.io/acme/app":                 "acme/app:latest",
		"localhost:5000/app":                 "localhost:5000/app:latest",
		"myreg:5000/app:2":                   "myreg:5000/app:2",
		"ghcr.io/acme/app@sha256:abc":        "ghcr.io/acme/app@sha256:abc",
		"docker.io/library/nginx@sha256:abc": "nginx@sha256:abc",
	} {
		if got := normalizeReference(in); got != want {
			t.Errorf("normalizeReference(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanMatchesImageReferenceHoweverSpelled(t *testing.T) {
	owners := Ownership{Images: map[string]string{"docker.io/library/nginx": "web", "myreg:5000/app": "api", "ghcr.io/a/b@sha256:abc": "pinned"}}
	images := []Candidate{
		{ID: "1", Name: "nginx:latest", Tags: []string{"nginx:latest"}},
		{ID: "2", Name: "myreg:5000/app:latest", Tags: []string{"myreg:5000/app:latest"}},
		{ID: "3", Name: "sha256:3", Digests: []string{"ghcr.io/a/b@sha256:abc"}},
		{ID: "4", Name: "myreg:5000/app:2", Tags: []string{"myreg:5000/app:2"}},
	}
	plan := Plan(UnusedImages, images, owners, false)
	if got := names(plan.Delete); !reflect.DeepEqual(got, []string{"myreg:5000/app:2"}) || len(plan.Skip) != 3 {
		t.Fatalf("plan: %+v", plan)
	}
}

func TestPlanSkipsPanelOwned(t *testing.T) {
	size := uint64(7)
	owners := Ownership{
		Projects: map[string]string{"relist-c2": "shop"},
		Volumes:  map[string]string{"shared-db": "shop"},
		Images:   map[string]string{"registry.local/shop:1": "shop", "nginx": "web"},
	}
	volumes := []Candidate{
		{Name: "relist-c2_data", Labels: map[string]string{"com.docker.compose.project": "relist-c2"}, Size: &size},
		{Name: "shared-db"},
		{Name: "foreign", Labels: map[string]string{"com.docker.compose.project": "other"}, Size: &size},
		{Name: "bare"},
	}
	plan := Plan(AllVolumes, volumes, owners, false)
	if got := names(plan.Delete); !reflect.DeepEqual(got, []string{"foreign", "bare"}) {
		t.Fatalf("delete = %v", got)
	}
	if plan.Delete[0].Size == nil || *plan.Delete[0].Size != 7 || plan.Delete[1].Size != nil {
		t.Fatalf("sizes: %+v", plan.Delete)
	}
	wantSkip := []SkipEntry{
		{Name: "relist-c2_data", Compose: "shop", Reason: "used by Compose shop"},
		{Name: "shared-db", Compose: "shop", Reason: "used by Compose shop"},
	}
	if !reflect.DeepEqual(plan.Skip, wantSkip) {
		t.Fatalf("skip = %+v", plan.Skip)
	}
	if plan.Kind != AllVolumes {
		t.Fatalf("kind = %s", plan.Kind)
	}

	plan = Plan(AllVolumes, volumes, owners, true)
	if len(plan.Delete) != 4 || len(plan.Skip) != 0 || plan.Skip == nil {
		t.Fatalf("includePanel: %+v", plan)
	}

	images := []Candidate{
		{ID: "sha256:a", Name: "registry.local/shop:1", Tags: []string{"registry.local/shop:1"}},
		{ID: "sha256:b", Name: "nginx:latest", Tags: []string{"nginx:latest"}},
		{ID: "sha256:c", Name: "built", Tags: []string{"built:1"}, Labels: map[string]string{"com.docker.compose.project": "relist-c2"}},
		{ID: "sha256:d", Name: "sha256:d"},
		{ID: "sha256:e", Name: "other:1", Tags: []string{"other:1"}},
	}
	plan = Plan(UnusedImages, images, owners, false)
	if got := names(plan.Delete); !reflect.DeepEqual(got, []string{"sha256:d", "other:1"}) {
		t.Fatalf("image delete = %v", got)
	}
	if len(plan.Skip) != 3 || plan.Skip[1].Compose != "web" {
		t.Fatalf("image skip = %+v", plan.Skip)
	}
}
