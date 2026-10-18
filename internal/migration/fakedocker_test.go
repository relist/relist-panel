package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"relist-panel/internal/composes"
)

// FakeDocker is an in-memory Docker for Migration tests. It is exported so
// the external integration test package can use it (export_test pattern).
// Volume and host path contents are opaque byte blobs: Pack writes the blob
// stored under mountSource|name, Unpack stores what it reads under
// mountSource. Images are keyed by every tag and by ID.
type FakeDocker struct {
	mu sync.Mutex

	Volumes  map[string]VolumeInfo
	Blobs    map[string][]byte // "mountSource|name" -> packed bytes, read by Pack
	Unpacked map[string][]byte // mountSource -> bytes Unpack received
	Sizes    map[string]int64  // "mountSource|name" -> Size answer
	FreeB    map[string]int64  // mountSource -> Free answer

	Images   map[string]ImageInfo // tag or ID -> image
	Pullable map[string]ImageInfo // "repo@sha256:..." -> image a pull installs
	Loadable map[string]ImageInfo // ref saved by SaveImages -> image Load installs

	Running         map[string]bool     // compose project -> has running Containers
	ContainerImages map[string][]string // compose project -> image IDs

	Maintenance   map[string]string
	MaintenanceUp bool
	TraefikUp     bool
	RootDir       string
	Fail          map[string]error // op name -> error to return
	Calls         []string
}

func NewFakeDocker() *FakeDocker {
	return &FakeDocker{
		Volumes: map[string]VolumeInfo{}, Blobs: map[string][]byte{}, Unpacked: map[string][]byte{},
		Sizes: map[string]int64{}, FreeB: map[string]int64{}, Images: map[string]ImageInfo{},
		Pullable: map[string]ImageInfo{}, Loadable: map[string]ImageInfo{}, Running: map[string]bool{},
		ContainerImages: map[string][]string{}, TraefikUp: true, RootDir: "/var/lib/docker", Fail: map[string]error{},
	}
}

var _ Docker = (*FakeDocker)(nil)

func (f *FakeDocker) call(op string, args ...string) error {
	f.Calls = append(f.Calls, strings.TrimSpace(op+" "+strings.Join(args, " ")))
	return f.Fail[op]
}

// CallsOf lists the recorded calls of one op.
func (f *FakeDocker) CallsOf(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.Calls {
		if c == op || strings.HasPrefix(c, op+" ") {
			out = append(out, c)
		}
	}
	return out
}

func (f *FakeDocker) AddImage(img ImageInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addImage(img)
}

func (f *FakeDocker) addImage(img ImageInfo) {
	f.Images[img.ID] = img
	for _, tag := range img.Tags {
		f.Images[tag] = img
	}
}

func (f *FakeDocker) ComposeStop(_ context.Context, req composes.RunRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("compose-stop", req.ProjectName); err != nil {
		return err
	}
	f.Running[req.ProjectName] = false
	return nil
}

func (f *FakeDocker) ComposeStart(_ context.Context, req composes.RunRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("compose-start", req.ProjectName); err != nil {
		return err
	}
	f.Running[req.ProjectName] = true
	return nil
}

func (f *FakeDocker) ProjectContainerImages(_ context.Context, project string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ContainerImages[project]), f.call("project-images", project)
}

func (f *FakeDocker) InspectVolume(_ context.Context, name string) (VolumeInfo, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Volumes[name]
	return v, ok, f.Fail["inspect-volume"]
}

func (f *FakeDocker) CreateVolume(_ context.Context, v VolumeInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("create-volume", v.Name); err != nil {
		return err
	}
	if _, ok := f.Volumes[v.Name]; ok {
		return fmt.Errorf("volume %s exists", v.Name)
	}
	f.Volumes[v.Name] = v
	return nil
}

func (f *FakeDocker) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Volumes, name)
	delete(f.Unpacked, name)
	return f.call("remove-volume", name)
}

func (f *FakeDocker) InspectImage(_ context.Context, ref string) (ImageInfo, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	img, ok := f.Images[ref]
	return img, ok, f.Fail["inspect-image"]
}

func (f *FakeDocker) PullImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("pull", ref); err != nil {
		return err
	}
	img, ok := f.Pullable[ref]
	if !ok {
		return fmt.Errorf("pull %s: not found", ref)
	}
	f.Images[img.ID] = img
	f.Images[ref] = img
	return nil
}

func (f *FakeDocker) TagImage(_ context.Context, source, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("tag", source, target); err != nil {
		return err
	}
	img, ok := f.Images[source]
	if !ok {
		return fmt.Errorf("tag: no image %s", source)
	}
	if !slices.Contains(img.Tags, target) {
		img.Tags = append(slices.Clone(img.Tags), target)
	}
	f.addImage(img)
	return nil
}

func (f *FakeDocker) SaveImages(_ context.Context, refs []string, w io.Writer) error {
	f.mu.Lock()
	err := f.call("save", refs...)
	f.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "images:"+strings.Join(refs, ","))
	return err
}

func (f *FakeDocker) LoadImages(_ context.Context, r io.Reader) ([]string, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("load"); err != nil {
		return nil, err
	}
	refs, ok := strings.CutPrefix(string(body), "images:")
	if !ok {
		return nil, errors.New("load: not an image archive")
	}
	var loaded []string
	for _, ref := range strings.Split(refs, ",") {
		img, ok := f.Loadable[ref]
		if !ok {
			return nil, fmt.Errorf("load: archive names unknown %s", ref)
		}
		f.addImage(img)
		if strings.HasPrefix(ref, "sha256:") {
			loaded = append(loaded, img.ID) // an untagged Image reports its ID on this host
		} else {
			loaded = append(loaded, ref)
		}
	}
	return loaded, nil
}

func (f *FakeDocker) RemoveImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Images, ref)
	return f.call("remove-image", ref)
}

func (f *FakeDocker) Pack(_ context.Context, mountSource, name string, w io.Writer) error {
	f.mu.Lock()
	blob, ok := f.Blobs[mountSource+"|"+name]
	err := f.call("pack", mountSource, name)
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pack: nothing at %s|%s", mountSource, name)
	}
	_, err = w.Write(blob)
	return err
}

func (f *FakeDocker) Unpack(_ context.Context, mountSource string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("unpack", mountSource); err != nil {
		return err
	}
	f.Unpacked[mountSource] = body
	return nil
}

func (f *FakeDocker) Delete(_ context.Context, mountSource, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Unpacked, mountSource)
	return f.call("delete", mountSource, name)
}

func (f *FakeDocker) Size(_ context.Context, mountSource, name string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Sizes[mountSource+"|"+name], f.call("size", mountSource, name)
}

func (f *FakeDocker) Free(_ context.Context, mountSource string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.FreeB[mountSource]
	if !ok {
		n = 1 << 50
	}
	return n, f.call("free", mountSource)
}

func (f *FakeDocker) DockerRootDir(context.Context) (string, error) { return f.RootDir, nil }

func (f *FakeDocker) RunMaintenance(_ context.Context, labels map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("run-maintenance"); err != nil {
		return err
	}
	f.Maintenance, f.MaintenanceUp = maps.Clone(labels), true
	return nil
}

func (f *FakeDocker) RemoveMaintenance(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Maintenance, f.MaintenanceUp = nil, false
	return f.call("remove-maintenance")
}

func (f *FakeDocker) MaintenanceRunning(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.MaintenanceUp, nil
}

func (f *FakeDocker) StopTraefik(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TraefikUp = false
	return f.call("stop-traefik")
}

func (f *FakeDocker) StartTraefik(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TraefikUp = true
	return f.call("start-traefik")
}
