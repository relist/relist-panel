package volumes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeCLI is a scripted volumes.CLI: it records every call and answers with
// the configured output, so the tests exercise parsing and pre-flight with no
// Docker in them.
type fakeCLI struct {
	configOutput []byte
	configErr    error
	configRefs   []ComposeRef

	volumeExists map[string]bool
	volumeErr    error
	volumeNames  []string
}

func (f *fakeCLI) ComposeConfig(_ context.Context, ref ComposeRef) ([]byte, error) {
	f.configRefs = append(f.configRefs, ref)
	return f.configOutput, f.configErr
}

func (f *fakeCLI) VolumeExists(_ context.Context, name string) (bool, error) {
	f.volumeNames = append(f.volumeNames, name)
	if f.volumeErr != nil {
		return false, f.volumeErr
	}
	return f.volumeExists[name], nil
}

// demoConfig is `docker compose config --format json`'s real shape (Docker
// 29), with the CLI's own work already done: db's relative bind came out
// absolute, the `${VAR}` default was interpolated, the top-level map carries
// pgdata's real name, and web's anonymous volume and tmpfs mount carry no
// source. db declares `user:`; web does not.
const demoConfig = `{
  "name": "demo",
  "services": {
    "db": {
      "image": "postgres:16",
      "user": "999:998",
      "volumes": [
        {"type": "volume", "source": "pgdata", "target": "/var/lib/postgresql/data", "volume": {}},
        {"type": "bind", "source": "/src/compose/rel", "target": "/rel", "read_only": true, "bind": {}},
        {"type": "bind", "source": "/srv/from-env", "target": "/srv", "bind": {}}
      ]
    },
    "web": {
      "image": "nginx:alpine",
      "volumes": [
        {"type": "volume", "source": "pgdata", "target": "/pgdata", "read_only": true, "volume": {}},
        {"type": "volume", "target": "/anon", "volume": {}},
        {"type": "tmpfs", "target": "/scratch"}
      ]
    }
  },
  "volumes": {
    "pgdata": {"name": "demo_pgdata"}
  }
}`

// Resolve reads services.*.volumes[] plus the top-level map, groups the same
// source across Services into one Volume, and applies the UID/GID rule — all
// from the CLI's output, with the CLI's ref passed through untouched.
func TestResolveReadsTheCLIsResolvedOutput(t *testing.T) {
	cli := &fakeCLI{configOutput: []byte(demoConfig)}
	resolver := NewResolver(cli, t.TempDir())
	ref := ComposeRef{
		ProjectName:     "demo",
		ComposeFilePath: "/data/demo/docker-compose.yml",
		EnvFilePath:     "/data/demo/.env",
	}

	got, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	want := Resolution{
		Volumes: []Volume{
			{
				Kind: VolumeNamed, LogicalName: "pgdata", Source: "demo_pgdata",
				Uses: []Use{
					{Service: "db", Target: "/var/lib/postgresql/data"},
					{Service: "web", Target: "/pgdata", ReadOnly: true},
				},
			},
			{
				Kind: VolumeBind, Source: "/src/compose/rel",
				Uses: []Use{{Service: "db", Target: "/rel", ReadOnly: true}},
			},
			{
				Kind: VolumeBind, Source: "/srv/from-env",
				Uses: []Use{{Service: "db", Target: "/srv"}},
			},
		},
		Users: []ServiceUser{
			{Service: "db", User: "999:998", Source: UserFromCompose},
			{Service: "web", User: "0:0", Source: UserFallbackRoot, Warning: noUserWarning},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve() =\n\t%+v\nwant\n\t%+v", got, want)
	}
	if len(cli.configRefs) != 1 || cli.configRefs[0] != ref {
		t.Errorf("ComposeConfig refs = %+v, want exactly %+v", cli.configRefs, ref)
	}
}

// A CLI failure is the CLI's own error, surfaced to the caller: before the
// first deploy that is `couldn't find env file`, and no YAML fallback is
// attempted.
func TestResolveSurfacesTheCLIsOwnError(t *testing.T) {
	cli := &fakeCLI{configErr: errors.New("docker compose -p demo config --format json: exit status 1: couldn't find env file: /data/demo/.env")}
	resolver := NewResolver(cli, t.TempDir())

	got, err := resolver.Resolve(context.Background(), ComposeRef{ProjectName: "demo"})
	if err == nil || !strings.Contains(err.Error(), "couldn't find env file") {
		t.Fatalf("Resolve() error = %v, want the CLI's couldn't find env file", err)
	}
	if len(got.Volumes) != 0 || len(got.Users) != 0 {
		t.Errorf("Resolve() = %+v, want no partial resolution on error", got)
	}
}

// Anything the real CLI would not have produced is an error, not a guess:
// mounting a plausible-looking wrong source is what this feature must never
// do.
func TestResolveRejectsOutputItCannotTrust(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		wantContains string
	}{
		{
			name:         "not JSON",
			output:       `this is not the CLI's output`,
			wantContains: "parse docker compose config output",
		},
		{
			name:         "a named volume with no top-level entry",
			output:       `{"services":{"db":{"volumes":[{"type":"volume","source":"pgdata","target":"/data"}]}}}`,
			wantContains: `volume "pgdata" has no top-level real name`,
		},
		{
			name:         "a named volume whose top-level entry has no name",
			output:       `{"services":{"db":{"volumes":[{"type":"volume","source":"pgdata","target":"/data"}]}},"volumes":{"pgdata":{}}}`,
			wantContains: `volume "pgdata" has no top-level real name`,
		},
		{
			name:         "a bind that is not absolute",
			output:       `{"services":{"db":{"volumes":[{"type":"bind","source":"rel","target":"/rel"}]}}}`,
			wantContains: "is not an absolute host path",
		},
		{
			name:         "a mount with no target",
			output:       `{"services":{"db":{"volumes":[{"type":"bind","source":"/srv/app"}]}}}`,
			wantContains: "names no target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewResolver(&fakeCLI{configOutput: []byte(tt.output)}, t.TempDir())

			_, err := resolver.Resolve(context.Background(), ComposeRef{})
			if err == nil || !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("Resolve() error = %v, want it to contain %q", err, tt.wantContains)
			}
		})
	}
}

// Mounts with no declared source — anonymous volumes and tmpfs — are not
// Volumes the Explorer can address, so they are skipped and never invented
// from anything else.
func TestResolveSkipsMountsWithNoSource(t *testing.T) {
	cli := &fakeCLI{configOutput: []byte(`{
		"services": {
			"db": {
				"volumes": [
					{"type": "volume", "target": "/anon", "volume": {}},
					{"type": "tmpfs", "target": "/scratch"},
					{"type": "npipe", "source": "//./pipe/docker_engine", "target": "/pipe"}
				]
			}
		}
	}`)}
	resolver := NewResolver(cli, t.TempDir())

	got, err := resolver.Resolve(context.Background(), ComposeRef{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got.Volumes) != 0 {
		t.Errorf("Resolve() volumes = %+v, want none", got.Volumes)
	}
	want := []ServiceUser{{Service: "db", User: "0:0", Source: UserFallbackRoot, Warning: noUserWarning}}
	if !reflect.DeepEqual(got.Users, want) {
		t.Errorf("Resolve() users = %+v, want %+v", got.Users, want)
	}
}
