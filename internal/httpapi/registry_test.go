package httpapi

import (
	"context"
	"testing"

	"relist-panel/internal/deployments"
	"relist-panel/internal/registry"
)

// The Docker page's cleanup must never offer the Registry's image or volume.
func TestOwnRegistryClaimsImageAndVolume(t *testing.T) {
	images, volumes := map[string]string{"nginx": "web"}, map[string]string{}
	ownRegistry(images, volumes)
	for _, ref := range registry.OwnedImageRefs(registry.DefaultImage) {
		if images[ref] != registry.OwnerName {
			t.Errorf("image %s owned by %q", ref, images[ref])
		}
	}
	if volumes[registry.VolumeName] != registry.OwnerName || images["nginx"] != "web" {
		t.Fatalf("images = %v, volumes = %v", images, volumes)
	}
}

func TestUpdateWaitingOnNamesRegistryCleanup(t *testing.T) {
	queue := deployments.NewQueue(deployments.Deps{})
	if got := updateWaitingOn(context.Background(), nil, queue, func() bool { return true }); got != "a Registry cleanup" {
		t.Fatalf("cleaning: waiting on %q", got)
	}
	if got := updateWaitingOn(context.Background(), nil, queue, func() bool { return false }); got != "a vulnerability scan" {
		t.Fatalf("not cleaning: waiting on %q", got)
	}
	if got := updateWaitingOn(context.Background(), nil, queue, nil); got != "a vulnerability scan" {
		t.Fatalf("no cleaner: waiting on %q", got)
	}
}
