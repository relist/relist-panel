package registry

import (
	"context"
	"strings"

	"relist-panel/internal/registryauth"
)

// OwnerName is who owns the Registry's image and volume on the Docker page,
// so its confirmed cleanup leaves them alone.
const OwnerName = "Registry"

// Auths makes the Service a registryauth.Source: the hidden system login for
// the Registry's hostname while it is enabled, so Composes pull
// <hostname>/<repo>:<tag> with no setup (ADR-0033).
func (s *Service) Auths(ctx context.Context) ([]registryauth.Auth, error) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil || !st.Enabled || st.Hostname == "" || st.SystemSecret == "" {
		return nil, err
	}
	return []registryauth.Auth{{Host: st.Hostname, Username: SystemUser, Secret: st.SystemSecret}}, nil
}

// ReservedHost is the Registry's hostname once one is saved, enabled or not:
// no Domain may take it, or enabling later would collide.
func (s *Service) ReservedHost(ctx context.Context) (string, bool) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil || st.Hostname == "" {
		return "", false
	}
	return st.Hostname, true
}

// OwnedImageRefs are the references the registry image shows on the host:
// repo:tag and repo@digest (the same shapes vulnscan.ScannerRefs gives).
func OwnedImageRefs(ref string) []string {
	name, digest, _ := strings.Cut(ref, "@")
	refs := []string{}
	repo := name
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		repo = name[:i]
		refs = append(refs, name)
	}
	if digest != "" {
		refs = append(refs, repo+"@"+digest)
	}
	return refs
}
