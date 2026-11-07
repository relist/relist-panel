package registry

import (
	"context"
	"slices"
	"testing"

	"relist-panel/internal/registryauth"
)

func TestAuthsEmptyWhenDisabled(t *testing.T) {
	f := newService(t)
	ctx := context.Background()
	if got, err := f.svc.Auths(ctx); err != nil || len(got) != 0 {
		t.Fatalf("never enabled: %v, %v", got, err)
	}
	f.enable(t)
	st, _ := f.store.Settings(ctx)
	got, err := f.svc.Auths(ctx)
	if err != nil || !slices.Equal(got, []registryauth.Auth{{Host: "registry.example.com", Username: SystemUser, Secret: st.SystemSecret}}) {
		t.Fatalf("enabled: %v, %v", got, err)
	}
	if _, err := f.svc.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Auths(ctx); len(got) != 0 {
		t.Fatalf("disabled: %v", got)
	}
}

func TestOwnershipIncludesRegistry(t *testing.T) {
	want := []string{"registry:3", "registry@sha256:38907510915c3f1604c7e83d6ab602ab3020d092f73bdaed8696773693ff768c"}
	if got := OwnedImageRefs(DefaultImage); !slices.Equal(got, want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}
	if OwnerName != "Registry" || VolumeName != "relist-registry" {
		t.Fatalf("owner %q, volume %q", OwnerName, VolumeName)
	}
}

func TestReservedHost(t *testing.T) {
	f := newService(t)
	ctx := context.Background()
	if _, ok := f.svc.ReservedHost(ctx); ok {
		t.Fatal("no hostname yet, but reserved")
	}
	_ = f.svc.Save(ctx, Settings{Hostname: "registry.example.com", TLS: TLSLetsEncrypt})
	if h, ok := f.svc.ReservedHost(ctx); !ok || h != "registry.example.com" {
		t.Fatalf("saved, disabled: %q %v", h, ok)
	}
}
