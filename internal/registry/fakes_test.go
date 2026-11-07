package registry

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeHost records the calls the service makes and holds one container.
type fakeHost struct {
	t             *testing.T
	calls         []string
	state         ContainerState
	gc            GCState
	pullErr       error
	created       []ContainerSpec
	volume        int64
	volumeAfterGC int64
	filesDir      string
	// filesAtCreate is whether config.yml existed when Create ran.
	filesAtCreate bool
	// readOnlyAtGC is whether config.yml was read-only when RunGC ran.
	readOnlyAtGC bool
	gcPolls      int
	restartErrs  []error
	gcExit       int
	gcLogs       string
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

// Restart fails with the next entry of restartErrs, if any.
func (f *fakeHost) Restart(context.Context) error {
	f.calls = append(f.calls, "Restart")
	if len(f.restartErrs) > 0 {
		err := f.restartErrs[0]
		f.restartErrs = f.restartErrs[1:]
		if err != nil {
			return err
		}
	}
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

// VolumeBytes reports volume, then volumeAfterGC once GC has run.
func (f *fakeHost) VolumeBytes(context.Context) (int64, error) {
	if f.volumeAfterGC != 0 && slices.Contains(f.calls, "RunGC") {
		return f.volumeAfterGC, nil
	}
	return f.volume, nil
}

func (f *fakeHost) RunGC(_ context.Context, image, filesDir string) error {
	f.calls = append(f.calls, "RunGC")
	f.readOnlyAtGC, _ = Files{Dir: filesDir}.ReadOnly()
	f.gc = GCState{Exists: true, Running: true}
	return nil
}

// GCState reports the GC container running for gcPolls more polls, then
// exited with gcExit and gcLogs.
func (f *fakeHost) GCState(context.Context) (GCState, error) {
	if f.gc.Running {
		if f.gcPolls > 0 {
			f.gcPolls--
		} else {
			f.gc = GCState{Exists: true, ExitCode: f.gcExit, Logs: f.gcLogs}
		}
	}
	return f.gc, nil
}

func (f *fakeHost) RemoveGC(context.Context) error {
	f.calls = append(f.calls, "RemoveGC")
	f.gc = GCState{}
	return nil
}
