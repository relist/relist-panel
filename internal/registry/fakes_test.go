package registry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeHost records the calls the service makes and holds one container.
type fakeHost struct {
	t        *testing.T
	calls    []string
	state    ContainerState
	gc       GCState
	pullErr  error
	created  []ContainerSpec
	volume   int64
	filesDir string
	// filesAtCreate is whether config.yml existed when Create ran.
	filesAtCreate bool
}

func (f *fakeHost) EnsureImage(_ context.Context, ref string) error {
	f.calls = append(f.calls, "EnsureImage "+ref)
	return f.pullErr
}

func (f *fakeHost) Inspect(context.Context) (ContainerState, error) {
	f.calls = append(f.calls, "Inspect")
	return f.state, nil
}

func (f *fakeHost) Create(_ context.Context, spec ContainerSpec) error {
	f.calls = append(f.calls, "Create")
	_, err := os.Stat(filepath.Join(spec.FilesDir, "config.yml"))
	f.filesAtCreate = err == nil
	f.created = append(f.created, spec)
	f.state = ContainerState{Exists: true, Running: true, Image: spec.Image, Hash: spec.Hash}
	return nil
}

func (f *fakeHost) Restart(context.Context) error {
	f.calls = append(f.calls, "Restart")
	f.state.Running = true
	return nil
}

func (f *fakeHost) Remove(context.Context) error {
	f.calls = append(f.calls, "Remove")
	f.state = ContainerState{}
	return nil
}

func (f *fakeHost) RemoveVolume(context.Context) error {
	f.calls = append(f.calls, "RemoveVolume")
	return nil
}

func (f *fakeHost) VolumeBytes(context.Context) (int64, error) { return f.volume, nil }

func (f *fakeHost) RunGC(_ context.Context, image, filesDir string) error {
	f.calls = append(f.calls, "RunGC")
	f.gc = GCState{Exists: true, Running: true}
	return nil
}

func (f *fakeHost) GCState(context.Context) (GCState, error) { return f.gc, nil }

func (f *fakeHost) RemoveGC(context.Context) error {
	f.calls = append(f.calls, "RemoveGC")
	f.gc = GCState{}
	return nil
}
