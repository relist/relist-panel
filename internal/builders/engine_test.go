package builders

import "testing"

func TestSpecFor(t *testing.T) {
	got := SpecFor(Builder{ID: 3, CPUsMilli: 1500, MemoryMB: 2048, MaxParallelism: 4}, "img@sha256:x")
	want := HostSpec{Name: "relist-builder-3", Image: "img@sha256:x", CPUQuota: 150000, CPUPeriod: 100000, MemoryBytes: 2 << 30, MaxParallelism: 4}
	if got != want {
		t.Fatalf("SpecFor = %+v, want %+v", got, want)
	}
}

func TestEffectiveImage(t *testing.T) {
	if got := EffectiveImage(""); got != DefaultBuildKitImage {
		t.Fatalf("EffectiveImage('') = %q", got)
	}
	if got := EffectiveImage("x/y@sha256:" + hex64); got != "x/y@sha256:"+hex64 {
		t.Fatalf("EffectiveImage(custom) = %q", got)
	}
}

const hex64 = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

func TestValidImageRef(t *testing.T) {
	for ref, want := range map[string]bool{
		DefaultBuildKitImage:                   true,
		"moby/buildkit@sha256:" + hex64:        true,
		"moby/buildkit:v0.34.2":                false,
		"moby/buildkit:v0.34.2@sha256:abc":     false,
		"Moby/buildkit@sha256:" + hex64:        false,
		"":                                     false,
		"moby/buildkit@sha256:" + hex64 + " x": false,
	} {
		if got := ValidImageRef(ref); got != want {
			t.Errorf("ValidImageRef(%q) = %v, want %v", ref, got, want)
		}
	}
}
