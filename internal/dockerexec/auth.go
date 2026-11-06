package dockerexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"relist-panel/internal/registryauth"
)

// authEnv is environ with DOCKER_CONFIG pointing at a fresh 0700 dir whose
// config.json holds src's logins (ADR-0034). buildx keeps its builders under
// the Docker config dir, so BUILDX_CONFIG is pinned to the original one, or
// every Builder would vanish for this call. cleanup removes the dir.
func authEnv(ctx context.Context, src registryauth.Source, environ []string) ([]string, func(), error) {
	auths, err := src.Auths(ctx)
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "relist-auth-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := registryauth.WriteConfig(dir, auths); err != nil {
		cleanup()
		return nil, nil, err
	}

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
	if !hasBuildx {
		original := dockerConfig
		if original == "" && home != "" {
			original = filepath.Join(home, ".docker")
		}
		if original != "" {
			env = append(env, "BUILDX_CONFIG="+filepath.Join(original, "buildx"))
		}
	}
	return append(env, "DOCKER_CONFIG="+dir), cleanup, nil
}
