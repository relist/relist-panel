package imageref

import (
	"slices"
	"testing"
)

func TestPinned(t *testing.T) {
	const d = "sha256:38907510915c3f1604c7e83d6ab602ab3020d092f73bdaed8696773693ff768c"
	for in, want := range map[string][]string{
		"registry:3@" + d:           {"registry:3", "registry@" + d},
		"localhost:5000/trivy@" + d: {"localhost:5000/trivy@" + d},
		"aquasec/trivy:0.76.1":      {"aquasec/trivy:0.76.1"},
		"busybox":                   {},
	} {
		if got := Pinned(in); !slices.Equal(got, want) {
			t.Errorf("Pinned(%q) = %q, want %q", in, got, want)
		}
	}
}
