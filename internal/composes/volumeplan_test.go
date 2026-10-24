package composes

import (
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
