package volumes

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
)

// ComposeRef identifies one deployed Compose to the Docker CLI: its project
// name and the resolved files on disk — the same identity composes.RunRequest
// carries (ADR-0002, ADR-0005). It is declared here so this package never
// depends on the composes package.
type ComposeRef struct {
	ProjectName     string
	ComposeFilePath string
	EnvFilePath     string
	// AllProfiles resolves the Compose with every profile enabled
	// (`--profile '*'`), so volumes only a profile-gated Service mounts are
	// included. Orphan derivation sets it; the Volume Explorer does not.
	AllProfiles bool
}

// CLI is the Docker CLI port resolution and pre-flight need. ComposeConfig
// returns `docker compose config --format json`'s raw bytes — parsing them is
// this package's job, never the adapter's — and VolumeExists is
// `docker volume inspect <name>` reduced to a boolean: false only for a
// volume that does not exist, with every other failure (a daemon that is down,
// a missing CLI) an error, so "gone" is never confused with "unreachable".
// ListProjectVolumes lists the volumes labelled with a Compose project, and
// RemoveVolume is a plain `docker volume rm` that never forces: a volume still
// in use fails with ErrVolumeInUse.
type CLI interface {
	ComposeConfig(ctx context.Context, ref ComposeRef) ([]byte, error)
	VolumeExists(ctx context.Context, name string) (bool, error)
	ListProjectVolumes(ctx context.Context, project string) ([]ProjectVolume, error)
	RemoveVolume(ctx context.Context, name string) error
}

// VolumeKind is how a Volume is realised on the host, matching the `type` the
// Compose CLI reports for it.
type VolumeKind string

const (
	// VolumeNamed is a Docker named volume; Source is its real name.
	VolumeNamed VolumeKind = "volume"
	// VolumeBind is a host path; Source is its resolved absolute host path.
	VolumeBind VolumeKind = "bind"
)

// Volume is one source a Compose mounts, grouped across its Services: the
// same named volume or host path declared by two Services is one Volume with
// two Uses. Source is exactly what an operation mounts with `-v` — a named
// volume's real name or a bind's resolved absolute host path — and it comes
// only from the parsed CLI output, never from free text. LogicalName is the
// named volume's name as written in the Compose file (its key in the
// top-level `volumes:` map); it is empty for a bind, whose resolved path is
// already the source the Compose file named.
type Volume struct {
	Kind        VolumeKind
	LogicalName string
	Source      string
	Uses        []Use
}

// Use is one Service's declaration of a Volume: the container-side target and
// whether that Service mounts it read-only. They live per Use rather than on
// Volume because two Services can mount one source differently.
type Use struct {
	Service  string
	Target   string
	ReadOnly bool
}

// UserSource says where a Service's operation identity came from.
type UserSource string

const (
	// UserFromCompose is the Service's own resolved `user:`.
	UserFromCompose UserSource = "compose"
	// UserFallbackRoot is the 0:0 fallback for a Service that declares no
	// `user:`; Warning carries the consequence.
	UserFallbackRoot UserSource = "fallback"
)

// rootUser is the explicit fallback identity: root is a deliberate opt-in,
// never a default (ADR-0010).
const rootUser = "0:0"

// noUserWarning is the visible warning for UserFallbackRoot — files the
// operation creates become root-owned, and a stack running as another uid may
// then fail to start.
const noUserWarning = "the service declares no user:, so operations run as 0:0 and files they create become root-owned"

// ServiceUser is the uid/gid an operation against a Service runs as, always
// explicit for `--user`. Source says where the value came from so the UI can
// show it; Warning is non-empty only for the root fallback.
type ServiceUser struct {
	Service string
	User    string
	Source  UserSource
	Warning string
}

// Resolution is one Compose's resolved read side: the Volumes it mounts, in
// first-use order, and every Service's operation identity, ordered by Service
// name.
type Resolution struct {
	Volumes []Volume
	Users   []ServiceUser
}

// Resolver answers the read-side questions about a Compose's Volumes: which
// sources it mounts and as whom an operation should run, resolved through the
// Compose CLI, and whether a source is safe to mount at all.
type Resolver struct {
	cli     CLI
	dataDir string
}

// NewResolver constructs a Resolver over the CLI port and the panel's data
// directory (ADR-0002) — the one host location bind-mounted at its identical
// path, and therefore the only bind whose existence the panel can verify.
func NewResolver(cli CLI, dataDir string) *Resolver {
	return &Resolver{cli: cli, dataDir: dataDir}
}

// Resolve runs the Compose CLI's `config --format json` for ref and parses
// its output. The CLI's own error is returned unchanged — before the first
// deploy it is `couldn't find env file`, and the caller shows that reason
// rather than guessing a resolution the CLI refused to give.
func (r *Resolver) Resolve(ctx context.Context, ref ComposeRef) (Resolution, error) {
	output, err := r.cli.ComposeConfig(ctx, ref)
	if err != nil {
		return Resolution{}, err
	}
	return parseComposeConfig(output)
}

// composeConfig is the subset of `docker compose config --format json` this
// package reads: each Service's resolved mounts and the top-level volumes map
// holding a named volume's real name.
type composeConfig struct {
	Services map[string]composeService `json:"services"`
	Volumes  map[string]composeVolume  `json:"volumes"`
}

type composeService struct {
	User    string         `json:"user"`
	Volumes []composeMount `json:"volumes"`
}

type composeMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type composeVolume struct {
	Name string `json:"name"`
}

// volumeKey groups mount declarations into one Volume: the same kind and
// resolved source is the same Volume, whichever Service declares it.
type volumeKey struct {
	kind   VolumeKind
	source string
}

// parseComposeConfig turns the CLI's resolved JSON into the Compose's Volumes
// and Service users. The CLI has already resolved relative binds against the
// Compose file's directory, `${VAR}` interpolation and pruning of unreferenced
// volumes, so nothing here parses YAML or touches a path.
func parseComposeConfig(output []byte) (Resolution, error) {
	var config composeConfig
	if err := json.Unmarshal(output, &config); err != nil {
		return Resolution{}, fmt.Errorf("parse docker compose config output: %w", err)
	}

	services := make([]string, 0, len(config.Services))
	for name := range config.Services {
		services = append(services, name)
	}
	sort.Strings(services)

	resolution := Resolution{
		Volumes: make([]Volume, 0),
		Users:   make([]ServiceUser, 0, len(services)),
	}
	for _, name := range services {
		resolution.Users = append(resolution.Users, serviceUser(name, config.Services[name].User))
	}

	firstUse := make(map[volumeKey]int)
	for _, name := range services {
		for _, mount := range config.Services[name].Volumes {
			if mount.Type != string(VolumeNamed) && mount.Type != string(VolumeBind) {
				continue // tmpfs and friends have no host source to browse
			}
			if mount.Source == "" {
				continue // an anonymous volume has no declared source
			}
			volume, err := resolveMount(mount, config.Volumes)
			if err != nil {
				return Resolution{}, err
			}
			key := volumeKey{kind: volume.Kind, source: volume.Source}
			i, ok := firstUse[key]
			if !ok {
				i = len(resolution.Volumes)
				firstUse[key] = i
				resolution.Volumes = append(resolution.Volumes, volume)
			}
			resolution.Volumes[i].Uses = append(resolution.Volumes[i].Uses, Use{
				Service:  name,
				Target:   mount.Target,
				ReadOnly: mount.ReadOnly,
			})
		}
	}
	return resolution, nil
}

// resolveMount resolves one mount declaration against the top-level volumes
// map. Anything the CLI would not have produced is an error rather than a
// guess: mounting a plausible-looking wrong source is exactly what the
// feature must not do.
func resolveMount(mount composeMount, volumes map[string]composeVolume) (Volume, error) {
	if mount.Target == "" {
		return Volume{}, fmt.Errorf("compose config mount %s:%q names no target", mount.Type, mount.Source)
	}
	switch mount.Type {
	case string(VolumeNamed):
		entry, ok := volumes[mount.Source]
		if !ok || entry.Name == "" {
			return Volume{}, fmt.Errorf("compose config volume %q has no top-level real name", mount.Source)
		}
		return Volume{Kind: VolumeNamed, LogicalName: mount.Source, Source: entry.Name}, nil
	case string(VolumeBind):
		if !filepath.IsAbs(mount.Source) {
			return Volume{}, fmt.Errorf("compose config bind source %q is not an absolute host path", mount.Source)
		}
		return Volume{Kind: VolumeBind, Source: mount.Source}, nil
	}
	return Volume{}, fmt.Errorf("compose config mount type %q is not a Volume", mount.Type)
}

// serviceUser applies the one UID/GID rule: the Service's own resolved
// `user:` when it declares one, otherwise 0:0 with a visible warning.
func serviceUser(service, user string) ServiceUser {
	if user == "" {
		return ServiceUser{Service: service, User: rootUser, Source: UserFallbackRoot, Warning: noUserWarning}
	}
	return ServiceUser{Service: service, User: user, Source: UserFromCompose}
}
