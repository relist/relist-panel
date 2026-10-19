package migration

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"syscall"

	"relist-panel/internal/config"
	"relist-panel/internal/database"
)

// SourceInfo is what the old host tells the new one before anything is
// locked, so every blocking check runs first.
type SourceInfo struct {
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
	Warnings          []string               `json:"warnings"`
}

// Info resolves every Compose the way Start will, so a resolution error
// shows up here, before the old host is locked.
func (s *Source) Info(ctx context.Context) (SourceInfo, error) {
	version, err := database.SchemaVersion(ctx, s.d.DB)
	if err != nil {
		return SourceInfo{}, err
	}
	paths, _, err := s.d.Builder.HostPathCandidates(ctx)
	if err != nil {
		return SourceInfo{}, err
	}
	m, err := s.d.Builder.Build(ctx, nil, nil)
	if err != nil {
		return SourceInfo{}, err
	}
	info := SourceInfo{
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
			info.Volumes = append(info.Volumes, it)
		case KindImage:
			info.Images = append(info.Images, it)
		}
	}
	if err := s.d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE status = 'queued'").Scan(&info.QueuedDeployments); err != nil {
		return SourceInfo{}, fmt.Errorf("count queued deployments: %w", err)
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

func (s *Source) reportedDataDir() string {
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
// host paths the operator selected.
func Evaluate(ctx context.Context, local LocalHost, src SourceInfo, selected []string) ([]Check, error) {
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
	if err := local.DB.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM admin_credentials) OR EXISTS (SELECT 1 FROM projects)").Scan(&data); err != nil {
		return nil, err
	}
	if data == 1 {
		block("This panel already has data. Migrate into a freshly installed panel")
	}
	var dockerNeed int64
	for _, v := range src.Volumes {
		dockerNeed += v.Size
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
		if p == "/" || p != filepath.Clean(p) || !filepath.IsAbs(p) || inside(p, local.DataDir) || inside(local.DataDir, p) ||
			inside(p, "/var/lib/docker") || inside("/var/lib/docker", p) {
			block(p + " holds this panel's data or Docker's storage and cannot be restored here")
			continue
		}
		parent := filepath.Dir(p)
		// A failed restore cleans the path up, so anything already there
		// must block, and so must not knowing.
		if state, err := local.Docker.Probe(ctx, parent, filepath.Base(p)); err != nil {
			block("Could not check " + p + " on this host: " + err.Error())
		} else if state == PathOccupied {
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
			block(fmt.Sprintf("Not enough free space in %s: need %s, have %s", path, humanBytes(need), humanBytes(have)))
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

func humanBytes(n int64) string {
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
