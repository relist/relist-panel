package dockerexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"relist-panel/internal/builders"
)

// Buildx drives Builders: buildx builders with the docker-container driver,
// one BuildKit container and one state volume each. The CLI owns the builder
// lifecycle; the Engine API reads containers, volumes and the host.
type Buildx struct {
	api *Engine
}

func NewBuildx(socketPath string) *Buildx {
	return &Buildx{api: NewEngine(socketPath)}
}

var _ builders.Engine = (*Buildx)(nil)

// buildxContainer is the container buildx names for the builder's one node.
func buildxContainer(name string) string { return "buildx_buildkit_" + name + "0" }

func stateVolume(name string) string { return buildxContainer(name) + "_state" }

type apiBuilderContainer struct {
	State      struct{ Running bool }
	Config     struct{ Image string }
	HostConfig struct {
		CpuQuota, CpuPeriod, Memory, MemorySwap int64
	}
}

// container returns the builder's container, or nil when it is missing.
func (b *Buildx) container(ctx context.Context, name string) (*apiBuilderContainer, error) {
	var c apiBuilderContainer
	err := b.api.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(buildxContainer(name))+"/json", &c)
	var apiErr *engineError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (b *Buildx) Inspect(ctx context.Context, name string) (builders.Inspection, error) {
	c, err := b.container(ctx, name)
	switch {
	case err != nil:
		return builders.Inspection{}, err
	case c == nil:
		return builders.Inspection{State: builders.StateMissing}, nil
	case c.State.Running:
		return builders.Inspection{State: builders.StateRunning, Image: c.Config.Image}, nil
	}
	return builders.Inspection{State: builders.StateStopped, Image: c.Config.Image}, nil
}

func (b *Buildx) Ensure(ctx context.Context, spec builders.HostSpec) error {
	c, err := b.container(ctx, spec.Name)
	if err != nil {
		return err
	}
	if c == nil {
		// An instance this CLI still knows would bring the container back
		// with its old options, so drop it; the state volume stays.
		_, _ = cli(ctx, "buildx", "rm", "--keep-state", spec.Name)
		if err := b.create(ctx, spec); err != nil {
			return err
		}
	} else if !b.known(ctx, spec.Name) {
		// A rebuilt panel has a fresh ~/.docker: creating attaches to the
		// existing container and keeps its cache.
		if err := b.create(ctx, spec); err != nil {
			return err
		}
	}
	if _, err := cli(ctx, "buildx", "inspect", "--bootstrap", spec.Name); err != nil {
		return err
	}
	// buildx create never changes an existing container's limits.
	if c != nil && (c.HostConfig.CpuQuota != spec.CPUQuota || c.HostConfig.CpuPeriod != spec.CPUPeriod ||
		c.HostConfig.Memory != spec.MemoryBytes || c.HostConfig.MemorySwap != spec.MemoryBytes) {
		mem := strconv.FormatInt(spec.MemoryBytes, 10)
		if _, err := cli(ctx, "update", "--cpu-quota", strconv.FormatInt(spec.CPUQuota, 10), "--cpu-period", strconv.FormatInt(spec.CPUPeriod, 10),
			"--memory", mem, "--memory-swap", mem, buildxContainer(spec.Name)); err != nil {
			return err
		}
	}
	return nil
}

func (b *Buildx) Recreate(ctx context.Context, spec builders.HostSpec) error {
	if !b.known(ctx, spec.Name) {
		if err := b.attach(ctx, spec.Name); err != nil {
			return err
		}
	}
	if _, err := cli(ctx, "buildx", "rm", "--keep-state", spec.Name); err != nil {
		return err
	}
	if err := b.create(ctx, spec); err != nil {
		return err
	}
	_, err := cli(ctx, "buildx", "inspect", "--bootstrap", spec.Name)
	return err
}

func (b *Buildx) Remove(ctx context.Context, name string) error {
	c, err := b.container(ctx, name)
	if err != nil {
		return err
	}
	if c != nil || b.known(ctx, name) {
		if !b.known(ctx, name) {
			if err := b.attach(ctx, name); err != nil {
				return err
			}
		}
		if _, err := cli(ctx, "buildx", "rm", name); err != nil && c != nil {
			return err
		}
	}
	// buildx rm leaves the volume when the container was already gone.
	err = b.api.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(stateVolume(name)), nil)
	var apiErr *engineError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		return nil
	}
	return err
}

func (b *Buildx) Pull(ctx context.Context, ref string) error {
	_, err := cli(ctx, "pull", ref)
	return err
}

func (b *Buildx) Prune(ctx context.Context, name string) error {
	_, err := cli(ctx, "buildx", "prune", "-af", "--builder", name)
	return err
}

func (b *Buildx) DiskUsage(ctx context.Context, name string) (int64, error) {
	var df struct {
		Volumes []struct {
			Name      string
			UsageData *struct{ Size int64 }
		}
	}
	if err := b.api.do(ctx, http.MethodGet, "/system/df?type=volume", &df); err != nil {
		return 0, err
	}
	for _, v := range df.Volumes {
		if v.Name == stateVolume(name) && v.UsageData != nil && v.UsageData.Size > 0 {
			return v.UsageData.Size, nil
		}
	}
	return 0, nil
}

func (b *Buildx) Capacity(ctx context.Context) (int, int64, error) {
	var info struct {
		NCPU     int
		MemTotal int64
	}
	err := b.api.do(ctx, http.MethodGet, "/info", &info)
	return info.NCPU, info.MemTotal, err
}

func (b *Buildx) ImageExists(ctx context.Context, ref string) (bool, error) {
	err := b.api.do(ctx, http.MethodGet, "/images/"+ref+"/json", nil)
	var apiErr *engineError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

// known reports whether this CLI has the builder instance in ~/.docker.
func (b *Buildx) known(ctx context.Context, name string) bool {
	_, err := cli(ctx, "buildx", "inspect", name)
	return err == nil
}

// attach makes the CLI know an existing builder so it can remove it.
func (b *Buildx) attach(ctx context.Context, name string) error {
	_, err := cli(ctx, "buildx", "create", "--name", name, "--driver", "docker-container")
	return err
}

func (b *Buildx) create(ctx context.Context, spec builders.HostSpec) error {
	mem := strconv.FormatInt(spec.MemoryBytes, 10)
	opts := fmt.Sprintf("image=%s,memory=%s,memory-swap=%s,cpu-quota=%d,cpu-period=%d", spec.Image, mem, mem, spec.CPUQuota, spec.CPUPeriod)
	args := []string{"buildx", "create", "--name", spec.Name, "--driver", "docker-container", "--driver-opt", opts}
	if spec.MaxParallelism > 0 {
		f, err := os.CreateTemp("", "relist-buildkitd-*.toml")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		_, err = fmt.Fprintf(f, "[worker.oci]\n  max-parallelism = %d\n", spec.MaxParallelism)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		args = append(args, "--buildkitd-config", f.Name())
	}
	_, err := cli(ctx, args...)
	return err
}

// cli runs one docker CLI command and returns its stdout; a failure carries
// the command and its stderr.
func cli(ctx context.Context, args ...string) (string, error) {
	cmd := command(ctx, args...)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
