package dockerexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"relist-panel/internal/registryauth"
)

// RegistryLogin implements registryauth.Tester with `docker login` against a
// throwaway DOCKER_CONFIG, so a test never leaves a login behind.
type RegistryLogin struct{}

const loginTimeout = 30 * time.Second

func (RegistryLogin) Login(ctx context.Context, a registryauth.Auth) error {
	dir, err := os.MkdirTemp("", "relist-login-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	host := a.Host
	if host == registryauth.DockerHub {
		host = "" // docker login with no server is Docker Hub
	}
	args := []string{"login", "--username", a.Username, "--password-stdin"}
	if host != "" {
		args = append(args, host)
	}
	cmd := command(ctx, args...)
	cmd.Env = append(cliEnv(os.Environ()), "DOCKER_CONFIG="+dir)
	cmd.Stdin = strings.NewReader(a.Secret)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("no answer from " + a.Host + " within 30s")
		}
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
