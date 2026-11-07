package migration

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"relist-panel/internal/config"
	"relist-panel/internal/database"
)

// OldHostInfo is what the old host tells the new one before anything is
// locked, so every blocking check runs first.
type OldHostInfo struct {
	Protocol          int                    `json:"protocol"`
	SchemaVersion     int64                  `json:"schemaVersion"`
	DataDir           string                 `json:"dataDir"`
	Install           config.InstallSettings `json:"install"`
	HostPaths         []HostPathCandidate    `json:"hostPaths"`
	Volumes           []Item                 `json:"volumes"`
	Images            []Item                 `json:"images"`
	DataDirSize       int64                  `json:"dataDirSize"`
	DatabaseSize      int64                  `json:"databaseSize"`
	ActiveDeployment  string                 `json:"activeDeployment,omitempty"`
	QueuedDeployments int                    `json:"queuedDeployments"`
	// Registry is the Panel registry's volume, offered as Include registry
	// data (off by default); nil when this host has none.
	Registry *Item    `json:"registry,omitempty"`
	Warnings []string `json:"warnings"`
}

// Info resolves every Compose the way Start will, so a resolution error
// shows up here, before the old host is locked.
func (s *OldHost) Info(ctx context.Context) (OldHostInfo, error) {
	version, err := database.SchemaVersion(ctx, s.d.DB)
	if err != nil {
		return OldHostInfo{}, err
	}
	paths, _, err := s.d.Manifests.HostPathCandidates(ctx)
	if err != nil {
		return OldHostInfo{}, err
	}
	m, err := s.d.Manifests.BuildWith(ctx, nil, nil, true)
	if err != nil {
		return OldHostInfo{}, err
	}
	info := OldHostInfo{
		Protocol: ProtocolVersion, SchemaVersion: version, DataDir: s.reportedDataDir(), Install: s.d.Install,
		HostPaths: paths, Volumes: []Item{}, Images: []Item{}, Warnings: m.Warnings,
	}
	if info.HostPaths == nil {
		info.HostPaths = []HostPathCandidate{}
	}
	for _, it := range m.Items {
		switch it.Kind {
		case KindDatabase:
			info.DatabaseSize = it.Size
		case KindDataDir:
			info.DataDirSize = it.Size
		case KindVolume:
			if it.Volume != nil && it.Volume.Name == RegistryVolume {
				registryItem := it
				info.Registry = &registryItem
				continue
			}
			info.Volumes = append(info.Volumes, it)
		case KindImage:
			info.Images = append(info.Images, it)
		}
	}
	if err := s.d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE status = 'queued'").Scan(&info.QueuedDeployments); err != nil {
		return OldHostInfo{}, fmt.Errorf("count queued deployments: %w", err)
	}
	if id, number, ok := s.d.Queue.Running(); ok {
		name := fmt.Sprintf("Compose %d", id)
		if target, err := s.d.Composes.Target(ctx, id); err == nil {
			name = target.Compose.Name
		}
		info.ActiveDeployment = fmt.Sprintf("%s #%d", name, number)
	}
	return info, nil
}

func (s *OldHost) reportedDataDir() string {
	if s.d.ReportedDataDir != "" {
		return s.d.ReportedDataDir
	}
	return s.d.DataDir
}

// Check is one preflight finding. A blocking one keeps Start disabled.
type Check struct {
	Blocking bool   `json:"blocking"`
	Message  string `json:"message"`
}

// LocalHost is the new host as preflight and restore see it. FreeBytes is
// statfs on a path the panel can see; nil means the real statfs.
type LocalHost struct {
	DB        *sql.DB
	Docker    Docker
	DataDir   string
	Install   config.InstallSettings
	FreeBytes func(path string) (int64, error)
}

func (l LocalHost) free(path string) (int64, error) {
	if l.FreeBytes != nil {
		return l.FreeBytes(path)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// Evaluate checks the new host against what the source reported, for the
// host paths the operator selected, without the Registry's data.
func Evaluate(ctx context.Context, local LocalHost, src OldHostInfo, selected []string) ([]Check, error) {
	return EvaluateWith(ctx, local, src, selected, false)
}

// EvaluateWith is Evaluate; includeRegistry checks the Registry volume like
// any other volume: its size, and that this host has none already.
func EvaluateWith(ctx context.Context, local LocalHost, src OldHostInfo, selected []string, includeRegistry bool) ([]Check, error) {
	if includeRegistry && src.Registry != nil && src.Registry.Volume != nil {
		src.Volumes = append(slices.Clone(src.Volumes), *src.Registry)
	}
	var checks []Check
	block := func(msg string) { checks = append(checks, Check{Blocking: true, Message: msg}) }
	warn := func(msg string) { checks = append(checks, Check{Message: msg}) }

	if src.Protocol != ProtocolVersion {
		block("The two panels speak different migration protocols. Use the same panel version on both")
	}
	known, err := database.KnownVersion()
	if err != nil {
		return nil, err
	}
	if known < src.SchemaVersion {
		block("This panel is older than the source. Upgrade it first")
	}
	if filepath.Clean(src.DataDir) != filepath.Clean(local.DataDir) {
		block("RELIST_DATA_DIR differs: set it to " + src.DataDir + " on this host")
	}
	var data int
	if err := local.DB.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM projects)").Scan(&data); err != nil {
		return nil, err
	}
	if data == 1 {
		block("This panel already has Projects. Migrate into a freshly installed panel")
	}
	var dockerNeed int64
	for _, v := range src.Volumes {
		dockerNeed += v.Size
		if reason := UnrestorableVolume(*v.Volume); reason != "" {
			block("Volume " + v.Volume.Name + " " + reason + " and cannot be restored here")
		}
		if _, ok, err := local.Docker.InspectVolume(ctx, v.Volume.Name); err != nil {
			return nil, err
		} else if ok {
			block("Volume " + v.Volume.Name + " already exists on this host")
		}
	}
	for _, img := range src.Images {
		dockerNeed += img.Size
	}
	parentNeed := map[string]int64{}
	var parents []string
	for _, p := range selected {
		i := slices.IndexFunc(src.HostPaths, func(c HostPathCandidate) bool { return c.Path == p })
		if i < 0 {
			block(p + " is not a host path the source mounts")
			continue
		}
		// Restoring over this panel's own data or Docker's storage would
		// break the host, whatever the source says about the path.
		if holdsStorage(p, local.DataDir) {
			block(p + " holds this panel's data or Docker's storage and cannot be restored here")
			continue
		}
		parent := filepath.Dir(p)
		// A failed restore cleans the path up, so anything already there
		// must block, and so must not knowing.
		if state, err := local.Docker.Probe(ctx, parent, filepath.Base(p)); err != nil {
			block("Could not check " + p + " on this host: " + err.Error())
		} else if state == PathOccupied || state == PathSpecial {
			block(p + " exists on this host and is not empty")
		}
		if _, ok := parentNeed[parent]; !ok {
			parents = append(parents, parent)
		}
		parentNeed[parent] += src.HostPaths[i].Size
	}

	space := func(path string, need int64, free func() (int64, error)) error {
		need = need + need/10
		have, err := free()
		if err != nil {
			return fmt.Errorf("free space in %s: %w", path, err)
		}
		if have < need {
			block(fmt.Sprintf("Not enough free space in %s: need %s, have %s", path, HumanBytes(need), HumanBytes(have)))
		}
		return nil
	}
	if err := space(local.DataDir, src.DataDirSize+src.DatabaseSize, func() (int64, error) { return local.free(local.DataDir) }); err != nil {
		return nil, err
	}
	root, err := local.Docker.DockerRootDir(ctx)
	if err != nil {
		return nil, err
	}
	if err := space(root, dockerNeed, func() (int64, error) { return local.Docker.Free(ctx, root) }); err != nil {
		return nil, err
	}
	for _, parent := range parents {
		if err := space(parent, parentNeed[parent], func() (int64, error) { return local.Docker.Free(ctx, parent) }); err != nil {
			return nil, err
		}
	}

	for _, f := range []struct{ env, old, cur string }{
		{"RELIST_DOMAIN", src.Install.Domain, local.Install.Domain},
		{"RELIST_ACME_EMAIL", src.Install.ACMEEmail, local.Install.ACMEEmail},
		{"TRAEFIK_HTTP_PORT", src.Install.HTTPPort, local.Install.HTTPPort},
		{"TRAEFIK_HTTPS_PORT", src.Install.HTTPSPort, local.Install.HTTPSPort},
	} {
		if f.old != "" && f.old != f.cur {
			cur := f.cur
			if cur == "" {
				cur = "unset"
			}
			warn(fmt.Sprintf("%s is %s on the source and %s here; copy it into this host's .env", f.env, f.old, cur))
		}
	}
	for _, w := range src.Warnings {
		warn(w)
	}
	if n := src.QueuedDeployments; n > 0 {
		noun := "Deployments"
		if n == 1 {
			noun = "Deployment"
		}
		warn(fmt.Sprintf("%d queued %s on the source will be canceled when the migration starts", n, noun))
	}
	if src.ActiveDeployment != "" {
		warn(src.ActiveDeployment + " is running on the source; queued Deployments there will be canceled")
	}
	if checks == nil {
		checks = []Check{}
	}
	return checks, nil
}

// UnrestorableVolume says why this host will not create v, or "". The old
// host never sends such a Volume; a driver or an option such as a bind
// device would mount whatever it names on this host.
func UnrestorableVolume(v VolumeInfo) string {
	if v.Driver != "" && v.Driver != "local" {
		return "uses driver " + v.Driver
	}
	if len(v.Options) > 0 {
		opts := make([]string, 0, len(v.Options))
		for k, val := range v.Options {
			opts = append(opts, k+"="+val)
		}
		slices.Sort(opts)
		return "has driver options (" + strings.Join(opts, ", ") + ")"
	}
	return ""
}

// HumanBytes formats a size the way preflight's messages do.
func HumanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	value, unit := float64(n), ""
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value, unit = value/1024, u
		if value < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

// holdsStorage reports a path that is or holds this panel's data or Docker's
// storage, or is not clean and absolute: restoring there would break the host.
func holdsStorage(p, dataDir string) bool {
	return p == "/" || p != filepath.Clean(p) || !filepath.IsAbs(p) || inside(p, dataDir) || inside(dataDir, p) ||
		inside(p, "/var/lib/docker") || inside("/var/lib/docker", p)
}
