package httpapi

import (
	"testing"

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
