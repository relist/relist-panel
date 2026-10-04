package volumes_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"relist-panel/internal/volumes"
	"relist-panel/internal/volumes/faketest"
)

// orphanConfig declares one named volume, app_data, as the current YAML.
const orphanConfig = `{"services":{"web":{"volumes":[{"type":"volume","source":"data","target":"/data"}]}},"volumes":{"data":{"name":"app_data"}}}`

var orphanRef = volumes.ComposeRef{ProjectName: "app", ComposeFilePath: "/c/docker-compose.yml", EnvFilePath: "/c/.env"}

// composeVolume is a listed volume carrying the labels Compose stamps on a
// volume it created for project.
func composeVolume(name, project, logical string) volumes.ProjectVolume {
	labels := map[string]string{volumes.ComposeProjectLabel: project}
	if logical != "" {
		labels[volumes.ComposeVolumeLabel] = logical
	}
	return volumes.ProjectVolume{Name: name, CreatedAt: "2026-01-02T03:04:05Z", Labels: labels}
}

func newOrphanFake(listed ...volumes.ProjectVolume) *faketest.CLI {
	return &faketest.CLI{ComposeConfigOutput: []byte(orphanConfig), ProjectVolumes: listed}
}

func orphanNames(orphans []volumes.ProjectVolume) []string {
	names := make([]string, 0, len(orphans))
	for _, o := range orphans {
		names = append(names, o.Name)
	}
	return names
}

func TestListOrphansExcludesVolumesInTheCurrentYAML(t *testing.T) {
	cli := newOrphanFake(composeVolume("app_data", "app", "data"), composeVolume("app_old", "app", "old"))

	got, err := volumes.NewResolver(cli, "/data").ListOrphans(context.Background(), orphanRef)
	if err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	if want := []string{"app_old"}; !reflect.DeepEqual(orphanNames(got), want) {
		t.Errorf("orphans = %v, want %v", orphanNames(got), want)
	}
	if want := []string{"app"}; !reflect.DeepEqual(cli.ListedProjects, want) {
		t.Errorf("ListedProjects = %v, want %v", cli.ListedProjects, want)
	}
}

func TestListOrphansIgnoresVolumesWhoseLabelIsAnotherProject(t *testing.T) {
	cli := newOrphanFake(composeVolume("other_x", "other", "x"))

	got, err := volumes.NewResolver(cli, "/data").ListOrphans(context.Background(), orphanRef)
	if err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("orphans = %#v, want an empty non-nil slice", got)
	}
}

func TestListOrphansIgnoresExplicitlyNamedAndUnlabelledVolumes(t *testing.T) {
	cli := newOrphanFake(
		composeVolume("shared_data", "app", "data"), // explicit `name:` — not <project>_<logical>
		composeVolume("app_nolabel", "app", ""),     // no com.docker.compose.volume label
	)

	got, err := volumes.NewResolver(cli, "/data").ListOrphans(context.Background(), orphanRef)
	if err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %v, want none", orphanNames(got))
	}
}

func TestListOrphansIsSortedByName(t *testing.T) {
	cli := newOrphanFake(
		composeVolume("app_c", "app", "c"),
		composeVolume("app_a", "app", "a"),
		composeVolume("app_b", "app", "b"),
	)

	got, err := volumes.NewResolver(cli, "/data").ListOrphans(context.Background(), orphanRef)
	if err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	if want := []string{"app_a", "app_b", "app_c"}; !reflect.DeepEqual(orphanNames(got), want) {
		t.Errorf("orphans = %v, want %v", orphanNames(got), want)
	}
}

func TestListOrphansSurfacesTheCLIFailure(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		cli  *faketest.CLI
	}{
		{"compose config", &faketest.CLI{ComposeConfigErr: boom, ProjectVolumes: []volumes.ProjectVolume{composeVolume("app_old", "app", "old")}}},
		{"volume list", &faketest.CLI{ComposeConfigOutput: []byte(orphanConfig), ListProjectVolumesErr: boom}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := volumes.NewResolver(tt.cli, "/data")

			got, err := resolver.ListOrphans(context.Background(), orphanRef)
			if !errors.Is(err, boom) {
				t.Fatalf("ListOrphans() error = %v, want the CLI failure", err)
			}
			if len(got) != 0 {
				t.Errorf("orphans = %v, want none on failure", orphanNames(got))
			}

			if err := resolver.RemoveOrphan(context.Background(), orphanRef, "app_old"); !errors.Is(err, boom) {
				t.Errorf("RemoveOrphan() error = %v, want the CLI failure", err)
			}
			if len(tt.cli.RemovedVolumes) != 0 {
				t.Errorf("RemovedVolumes = %v, want none", tt.cli.RemovedVolumes)
			}
		})
	}
}

func TestRemoveOrphanRefusesWhatIsNotAnOrphan(t *testing.T) {
	cli := newOrphanFake(
		composeVolume("app_data", "app", "data"),
		composeVolume("app_old", "app", "old"),
		composeVolume("other_x", "other", "x"),
	)
	resolver := volumes.NewResolver(cli, "/data")

	for _, name := range []string{"app_data", "other_x", "../x", "missing"} {
		if err := resolver.RemoveOrphan(context.Background(), orphanRef, name); !errors.Is(err, volumes.ErrNotOrphan) {
			t.Errorf("RemoveOrphan(%q) error = %v, want ErrNotOrphan", name, err)
		}
	}
	if len(cli.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none", cli.RemovedVolumes)
	}
}

func TestRemoveOrphanRemovesOnlyTheNamedOrphan(t *testing.T) {
	cli := newOrphanFake(
		composeVolume("app_data", "app", "data"),
		composeVolume("app_old", "app", "old"),
		composeVolume("app_older", "app", "older"),
	)

	if err := volumes.NewResolver(cli, "/data").RemoveOrphan(context.Background(), orphanRef, "app_old"); err != nil {
		t.Fatalf("RemoveOrphan() error = %v", err)
	}
	if want := []string{"app_old"}; !reflect.DeepEqual(cli.RemovedVolumes, want) {
		t.Errorf("RemovedVolumes = %v, want %v", cli.RemovedVolumes, want)
	}
}

func TestRemoveOrphanPassesTheInUseErrorThrough(t *testing.T) {
	cli := newOrphanFake(composeVolume("app_old", "app", "old"))
	cli.RemoveVolumeErr = volumes.ErrVolumeInUse

	err := volumes.NewResolver(cli, "/data").RemoveOrphan(context.Background(), orphanRef, "app_old")
	if !errors.Is(err, volumes.ErrVolumeInUse) {
		t.Errorf("RemoveOrphan() error = %v, want ErrVolumeInUse", err)
	}
}

// Orphan derivation resolves the Compose with every profile enabled, so a
// volume only a profile-gated service mounts still counts as declared — and
// the caller's ref is left untouched.
func TestOrphanDerivationResolvesWithEveryProfileEnabled(t *testing.T) {
	cli := newOrphanFake(composeVolume("app_old", "app", "old"))
	resolver := volumes.NewResolver(cli, "/data")

	if _, err := resolver.ListOrphans(context.Background(), orphanRef); err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	if err := resolver.RemoveOrphan(context.Background(), orphanRef, "app_old"); err != nil {
		t.Fatalf("RemoveOrphan() error = %v", err)
	}
	if len(cli.ComposeConfigRefs) != 2 {
		t.Fatalf("ComposeConfig calls = %d, want 2", len(cli.ComposeConfigRefs))
	}
	for i, ref := range cli.ComposeConfigRefs {
		if !ref.AllProfiles {
			t.Errorf("ComposeConfig ref %d AllProfiles = false, want true", i)
		}
	}
	if orphanRef.AllProfiles {
		t.Error("the caller's ref was mutated")
	}
}

// A volume mounted only by a profile-gated service is in the all-profiles
// resolution, so it is not an orphan and RemoveOrphan refuses it.
func TestProfileGatedVolumeIsNotAnOrphan(t *testing.T) {
	cli := newOrphanFake(composeVolume("app_tools", "app", "tools"), composeVolume("app_old", "app", "old"))
	cli.ComposeConfigOutput = []byte(`{"services":{"tools":{"volumes":[{"type":"volume","source":"tools","target":"/t"}]}},"volumes":{"tools":{"name":"app_tools"}}}`)
	resolver := volumes.NewResolver(cli, "/data")

	got, err := resolver.ListOrphans(context.Background(), orphanRef)
	if err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	if want := []string{"app_old"}; !reflect.DeepEqual(orphanNames(got), want) {
		t.Errorf("orphans = %v, want %v", orphanNames(got), want)
	}
	if err := resolver.RemoveOrphan(context.Background(), orphanRef, "app_tools"); !errors.Is(err, volumes.ErrNotOrphan) {
		t.Errorf("RemoveOrphan() error = %v, want ErrNotOrphan", err)
	}
	if len(cli.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none", cli.RemovedVolumes)
	}
}
