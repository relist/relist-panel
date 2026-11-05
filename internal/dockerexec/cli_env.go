package dockerexec

import "strings"

// cliEnv is the environment the docker CLI runs with: what it needs to find
// the daemon, its config and a temp dir, and nothing of the panel's own.
// `docker compose` interpolates its process environment into a Compose file,
// so inheriting it would let `${RELIST_ADMIN_PASSWORD}` reach Preview and
// containers (ADR-0031). Variables the panel means to pass, like
// BUILDX_BUILDER, are appended by the caller.
func cliEnv(environ []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "XDG_CONFIG_HOME": true, "XDG_RUNTIME_DIR": true, "BUILDX_CONFIG": true}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if keep[name] || strings.HasPrefix(name, "DOCKER_") {
			out = append(out, kv)
		}
	}
	return out
}
