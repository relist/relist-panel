package dockerexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"relist-panel/internal/registryauth"
)

// authEnv is environ with DOCKER_CONFIG pointing at a fresh 0700 dir whose
// config.json is the original one plus src's logins, without credsStore and
// without credHelpers for those hosts (ADR-0034). The original's contexts and
// cli-plugins dirs are linked in. buildx keeps its builders under the Docker
// config dir, so BUILDX_CONFIG is pinned to the original one, or every
// Builder would vanish for this call. With no logins, environ comes back
// unchanged. cleanup removes the dir, not what the links point at.
func authEnv(ctx context.Context, src registryauth.Source, environ []string) ([]string, func(), error) {
	auths, err := src.Auths(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(auths) == 0 {
		// Nothing to add: the call keeps its own config, credential store
		// included.
		return environ, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "relist-auth-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	var home, dockerConfig string
	hasBuildx := false
	env := make([]string, 0, len(environ)+2)
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		switch name {
		case "DOCKER_CONFIG":
			dockerConfig = value
			continue
		case "HOME":
			home = value
		case "BUILDX_CONFIG":
			hasBuildx = true
		}
		env = append(env, kv)
	}
	original := dockerConfig
	if original == "" && home != "" {
		original = filepath.Join(home, ".docker")
	}
	var base []byte
	if original != "" {
		base, _ = os.ReadFile(filepath.Join(original, "config.json")) // unreadable: start empty
		// Contexts and CLI plugins live under the config dir too.
		for _, sub := range []string{"contexts", "cli-plugins"} {
			if info, err := os.Stat(filepath.Join(original, sub)); err == nil && info.IsDir() {
				if err := os.Symlink(filepath.Join(original, sub), filepath.Join(dir, sub)); err != nil {
					cleanup()
					return nil, nil, err
				}
			}
		}
	}
	if err := registryauth.WriteConfigOver(dir, auths, base); err != nil {
		cleanup()
		return nil, nil, err
	}
	if !hasBuildx && original != "" {
		env = append(env, "BUILDX_CONFIG="+filepath.Join(original, "buildx"))
	}
	return append(env, "DOCKER_CONFIG="+dir), cleanup, nil
}
