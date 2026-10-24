package composes

import (
	"context"
	"reflect"
	"testing"
)

func TestDeclaredVolumeNamesSkipsInterpolated(t *testing.T) {
	yaml := `services:
  app:
    image: nginx
volumes:
  db:
    name: shared-db
  interpolated:
    name: ${PREFIX}-data
  mixed:
    name: "x-$PREFIX"
  ext:
    external: true
    name: outside
  legacy-ext:
    external:
      name: older
  unnamed: {}
  empty:
`
	got := declaredVolumeNames(yaml)
	if want := []string{"shared-db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("declaredVolumeNames = %v, want %v", got, want)
	}
	if got := declaredVolumeNames("services: ["); got != nil {
		t.Fatalf("invalid YAML = %v, want nil", got)
	}
}

func TestVolumeNamesElsewhereExcludesEveryTornDownCompose(t *testing.T) {
	svc, _, _, db := newDeployService(t, "services: {}\n")
	for id, yaml := range map[int]string{
		2: "volumes:\n  a:\n    name: shared-db\n",
		3: "volumes:\n  a:\n    name: shared-db\n  b:\n    name: only-three\n",
		4: "volumes:\n  a:\n    name: outside\n  b:\n    name: shared-db\n",
	} {
		if _, err := db.Exec(`INSERT INTO composes (id, environment_id, name, project_slug, env_slug, yaml_content, identity_version, created_at, updated_at) VALUES (?, 3, ?, 'p', 'e', ?, 1, 0, 0)`, id, "c"+string(rune('0'+id)), yaml); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.volumeNamesElsewhere(context.Background(), []int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"outside": "c4", "shared-db": "c4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("elsewhere = %v, want %v", got, want)
	}
	if got, _ := svc.volumeNamesElsewhere(context.Background(), []int64{1, 2}); got["shared-db"] != "c3" || got["only-three"] != "c3" {
		t.Fatalf("elsewhere = %v, want shared-db and only-three declared by c3", got)
	}
}
