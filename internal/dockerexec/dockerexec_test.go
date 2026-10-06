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

// A Compose's Service names ride its status for the terminal listing, in
// first-seen order and without duplicates across a Service's Containers.
func TestParseStatusCollectsServiceNames(t *testing.T) {
	output := `{"Service":"app","State":"running"}
{"Service":"worker","State":"exited"}
{"Service":"app","State":"running"}`
	status, err := parseStatus(output)
	if err != nil {
		t.Fatalf("parseStatus() error = %v", err)
	}
	if status.Running != 2 || status.Total != 3 {
		t.Fatalf("status counts = %+v, want running 2 of 3", status)
	}
	if want := []string{"app", "worker"}; !reflect.DeepEqual(status.Services, want) {
		t.Fatalf("Services = %v, want %v", status.Services, want)
	}
}
