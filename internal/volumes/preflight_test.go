package volumes

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The named-volume rung: an exact `docker volume inspect`, so a volume
// deleted underneath the operator is reported rather than silently recreated
// empty.
func TestPreflightNamedVolume(t *testing.T) {
	tests := []struct {
		name        string
		exists      bool
		checkErr    error
		wantStatus  PreflightStatus
		errContains string
	}{
		{
			name:       "an existing volume is ok",
			exists:     true,
			wantStatus: PreflightOK,
		},
		{
			name:       "a deleted volume is refused",
			exists:     false,
			wantStatus: PreflightMissing,
		},
		{
			name:        "a failed check surfaces as an error, not as missing",
			checkErr:    errors.New("docker volume inspect demo_pgdata: exit status 1: failed to connect to the docker API"),
			errContains: "failed to connect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &fakeCLI{volumeExists: map[string]bool{"demo_pgdata": tt.exists}, volumeErr: tt.checkErr}
			resolver := NewResolver(cli, t.TempDir())

			got, err := resolver.Preflight(context.Background(), Volume{Kind: VolumeNamed, Source: "demo_pgdata"})
			if tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("Preflight() error = %v, want it to contain %q", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("Preflight() error = %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Preflight() status = %q, want %q", got.Status, tt.wantStatus)
			}
			switch tt.wantStatus {
			case PreflightOK:
				if got.Warning != "" {
					t.Errorf("Preflight() warning = %q, want none", got.Warning)
				}
			case PreflightMissing:
				if !strings.Contains(got.Warning, "does not exist") {
					t.Errorf("Preflight() warning = %q, want it to say the volume does not exist", got.Warning)
				}
			}
			if !reflect.DeepEqual(cli.volumeNames, []string{"demo_pgdata"}) {
				t.Errorf("VolumeExists calls = %q, want exactly [demo_pgdata]", cli.volumeNames)
			}
		})
	}
}

// The bind-inside-the-data-directory rung: os.Stat against the identical-path
// mount (ADR-0002), checked before every operation and refused when absent.
func TestPreflightBindInsideTheDataDirectory(t *testing.T) {
	dataDir := t.TempDir()
	present := filepath.Join(dataDir, "app", "uploads")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatalf("create bind source: %v", err)
	}
	missing := filepath.Join(dataDir, "app", "retired")

	tests := []struct {
		name   string
		source string
		want   PreflightStatus
	}{
		{name: "an existing source is ok", source: present, want: PreflightOK},
		{name: "a missing source is refused", source: missing, want: PreflightMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &fakeCLI{}
			resolver := NewResolver(cli, dataDir)

			got, err := resolver.Preflight(context.Background(), Volume{Kind: VolumeBind, Source: tt.source})
			if err != nil {
				t.Fatalf("Preflight() error = %v", err)
			}
			if got.Status != tt.want {
				t.Errorf("Preflight() status = %q, want %q", got.Status, tt.want)
			}
			switch tt.want {
			case PreflightOK:
				if got.Warning != "" {
					t.Errorf("Preflight() warning = %q, want none", got.Warning)
				}
			case PreflightMissing:
				if !strings.Contains(got.Warning, "does not exist") {
					t.Errorf("Preflight() warning = %q, want it to say the source does not exist", got.Warning)
				}
			}
			if len(cli.volumeNames) != 0 {
				t.Errorf("VolumeExists called for a bind: %q", cli.volumeNames)
			}
		})
	}

	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%q) error = %v, want fs.ErrNotExist: pre-flight must not create the source", missing, err)
	}
}

// The third outcome: a bind outside the data directory cannot be checked —
// only a mount would tell, and a mount would create it — so it is reported
// unverifiable with the root:root 0755 consequence spelled out.
func TestPreflightBindOutsideTheDataDirectory(t *testing.T) {
	dataDir := t.TempDir()

	// The second source shares a string prefix with the data directory but is
	// not inside it; a naive prefix check would get it wrong.
	for _, source := range []string{"/srv/legacy-images", dataDir + "-sibling"} {
		cli := &fakeCLI{}
		resolver := NewResolver(cli, dataDir)

		got, err := resolver.Preflight(context.Background(), Volume{Kind: VolumeBind, Source: source})
		if err != nil {
			t.Fatalf("Preflight(%q) error = %v", source, err)
		}
		if got.Status != PreflightUnverifiable {
			t.Errorf("Preflight(%q) status = %q, want %q", source, got.Status, PreflightUnverifiable)
		}
		for _, want := range []string{"outside the data directory", "root:root", "0755"} {
			if !strings.Contains(got.Warning, want) {
				t.Errorf("Preflight(%q) warning = %q, want it to contain %q", source, got.Warning, want)
			}
		}
		if len(cli.volumeNames) != 0 {
			t.Errorf("Preflight(%q) called VolumeExists: %q", source, cli.volumeNames)
		}
	}
}

func TestPreflightRejectsAnUnknownKind(t *testing.T) {
	resolver := NewResolver(&fakeCLI{}, t.TempDir())

	if _, err := resolver.Preflight(context.Background(), Volume{Kind: "tmpfs", Source: "/scratch"}); err == nil {
		t.Error("Preflight() = nil error, want an unknown kind refusal")
	}
}
