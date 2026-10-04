package dockerexec

import (
	"reflect"
	"testing"
)

func TestUpArgsRemoveOrphanContainersButNeverVolumes(t *testing.T) {
	got := upArgs()
	want := []string{"up", "-d", "--build", "--remove-orphans"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs() = %v, want %v", got, want)
	}
	for _, arg := range got {
		if arg == "-v" || arg == "--volumes" {
			t.Fatalf("upArgs() contains %q; deploy must never remove volumes", arg)
		}
	}
}
