package panelupdate

import (
	"context"
	"os/exec"
	"testing"
)

const embeddedFile = "services:\n  panel:\n    image: a\n  traefik:\n    image: b\n\n  other:\n    image: c\n"

const newerFile = "services:\n  panel:\n    image: a2\n  traefik:\n    image: b\n\n  other:\n    image: c\n"

// fakeDocker answers the Engine API calls of a Preflight.
type fakeDocker struct {
	pulled  []string
	pullErr error
	hashes  []ServiceHash
	imageID string // the pulled image's ID; "sha256:target" when empty
}

func (f *fakeDocker) Pull(_ context.Context, ref string) error {
	f.pulled = append(f.pulled, ref)
	return f.pullErr
}

func (f *fakeDocker) ImageID(_ context.Context, ref string) (string, error) {
	if f.imageID != "" {
		return f.imageID, nil
	}
	return "sha256:target", nil
}

func (f *fakeDocker) ConfigHashes(context.Context, string) ([]ServiceHash, error) {
	return f.hashes, nil
}

var allInvariants = []m{
	{"kind": "service-image", "service": "panel", "image": fixtureImage},
	{"kind": "service-mount", "service": "panel", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock"},
	{"kind": "service-mount", "service": "panel", "source": "/proc", "target": "/host/proc", "read_only": true},
	{"kind": "data-dir-same-path", "service": "panel"},
	{"kind": "network-external", "network": "relist-network", "name": "relist-network"},
	{"kind": "service-network", "service": "panel", "network": "relist-network"},
	{"kind": "service-command-has", "service": "traefik", "arg": "--providers.file.directory=/letsencrypt/dynamic"},
	{"kind": "service-label", "service": "panel", "key": "traefik.http.routers.panel.priority", "value": "100000"},
}

func TestMergeFileExitCodes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	merged, n, err := mergeFile([]byte("a\nb\nc\n"), []byte("a\nb\nc\n"), []byte("a\nB\nc\n"))
	if err != nil || n != 0 || string(merged) != "a\nB\nc\n" {
		t.Fatalf("clean merge = %q, %d, %v", merged, n, err)
	}
	_, n, err = mergeFile([]byte("a\nX\nc\nd\ne\nf\ng\nY\n"), []byte("a\nb\nc\nd\ne\nf\ng\nh\n"), []byte("a\nB\nc\nd\ne\nf\ng\nH\n"))
	if err != nil || n != 2 {
		t.Fatalf("conflicts = %d, %v", n, err)
	}
}
