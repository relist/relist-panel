package databases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// fakeHost is an in-memory Host. It records every call as a string, keeps
// containers and volumes in maps and fails the calls named in failOn.
type fakeHost struct {
	mu         sync.Mutex
	calls      []string
	containers map[string]*ContainerState
	specs      map[string]ContainerSpec
	volumes    map[string]bool
	images     map[string]string // ref -> image ID after a pull
	failOn     map[string]error  // call prefix -> error
	logs       string
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		containers: map[string]*ContainerState{},
		specs:      map[string]ContainerSpec{},
		volumes:    map[string]bool{},
		images:     map[string]string{},
		failOn:     map[string]error{},
	}
}

func (f *fakeHost) record(call string) error {
	f.calls = append(f.calls, call)
	for prefix, err := range f.failOn {
		if strings.HasPrefix(call, prefix) {
			return err
		}
	}
	return nil
}

// callsExceptInspect is the call log without read-only calls.
func (f *fakeHost) mutating() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "inspect ") && !strings.HasPrefix(c, "volume-exists ") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeHost) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeHost) saw(call string) bool { return slices.Contains(f.all(), call) }

func (f *fakeHost) count(prefix string) int {
	n := 0
	for _, c := range f.all() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeHost) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *fakeHost) EnsureImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record("ensure " + ref)
}

func (f *fakeHost) PullImage(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("pull " + ref); err != nil {
		return "", err
	}
	id := f.images[ref]
	if id == "" {
		id = "sha256:img-" + ref
	}
	return id, nil
}

func (f *fakeHost) ImageID(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "sha256:img-" + ref, nil
}

func (f *fakeHost) Inspect(_ context.Context, name string) (ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("inspect " + name); err != nil {
		return ContainerState{}, err
	}
	if c, ok := f.containers[name]; ok {
		return *c, nil
	}
	return ContainerState{}, nil
}

func (f *fakeHost) VolumeExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("volume-exists " + name); err != nil {
		return false, err
	}
	return f.volumes[name], nil
}

func (f *fakeHost) Create(_ context.Context, spec ContainerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("create " + spec.Name); err != nil {
		return err
	}
	f.volumes[spec.Volume] = true
	f.specs[spec.Name] = spec
	st := &ContainerState{Exists: true, Running: true, Health: "healthy", Hash: spec.Labels[HashLabel], ImageID: "sha256:img-" + spec.Image}
	if id, ok := f.images[spec.Image]; ok {
		st.ImageID = id
	}
	if spec.Alias != "" {
		st.Aliases = []string{spec.Alias}
	}
	f.containers[spec.Name] = st
	return nil
}

func (f *fakeHost) with(name, call string, fn func(*ContainerState)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(call + " " + name); err != nil {
		return err
	}
	c, ok := f.containers[name]
	if !ok {
		return errors.New("docker: no such container " + name)
	}
	fn(c)
	return nil
}

func (f *fakeHost) Start(_ context.Context, name string) error {
	return f.with(name, "start", func(c *ContainerState) { c.Running = true })
}
func (f *fakeHost) Stop(_ context.Context, name string) error {
	return f.with(name, "stop", func(c *ContainerState) { c.Running = false })
}
func (f *fakeHost) Restart(_ context.Context, name string) error {
	return f.with(name, "restart", func(c *ContainerState) { c.Running = true })
}
func (f *fakeHost) Realias(_ context.Context, name, alias string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(fmt.Sprintf("realias %s %s", name, alias)); err != nil {
		return err
	}
	c, ok := f.containers[name]
	if !ok {
		return errors.New("docker: no such container " + name)
	}
	c.Aliases = nil
	if alias != "" {
		c.Aliases = []string{alias}
	}
	return nil
}

func (f *fakeHost) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("remove " + name); err != nil {
		return err
	}
	delete(f.containers, name)
	return nil
}

func (f *fakeHost) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("remove-volume " + name); err != nil {
		return err
	}
	delete(f.volumes, name)
	return nil
}

func (f *fakeHost) Logs(_ context.Context, name string, tail int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(fmt.Sprintf("logs %s %d", name, tail)); err != nil {
		return "", err
	}
	return f.logs, nil
}
