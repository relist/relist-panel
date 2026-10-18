package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/composes"
	"relist-panel/internal/database"
	"relist-panel/internal/domains"
	"relist-panel/internal/volumes"
)

// ProtocolVersion is the Migration wire protocol. Both panels must agree.
const ProtocolVersion = 1

type ItemKind string

const (
	KindDatabase ItemKind = "database"
	KindDataDir  ItemKind = "data-dir"
	KindVolume   ItemKind = "volume"
	KindHostPath ItemKind = "host-path"
	KindImage    ItemKind = "image"
)

// Item is one thing the new host pulls and restores on its own, with its own
// digest and retries.
type Item struct {
	ID       string      `json:"id"`
	Kind     ItemKind    `json:"kind"`
	Size     int64       `json:"size"`
	Volume   *VolumeInfo `json:"volume,omitempty"`
	HostPath string      `json:"hostPath,omitempty"`
	Image    *ImageInfo  `json:"image,omitempty"`
}

// HostPathCandidate is one bind source outside the data directory that the
// operator may tick. System paths are unticked by default.
type HostPathCandidate struct {
	Path     string   `json:"path"`
	System   bool     `json:"system"`
	Size     int64    `json:"size"`
	Composes []string `json:"composes"`
}

// Manifest is the fixed list of items, built after every Compose stopped.
type Manifest struct {
	Protocol          int      `json:"protocol"`
	SchemaVersion     int64    `json:"schemaVersion"`
	DataDir           string   `json:"dataDir"`
	RunningComposeIDs []int64  `json:"runningComposeIds"`
	Items             []Item   `json:"items"`
	Warnings          []string `json:"warnings"`
}

// ComposeTargets resolves a Compose ID to its row and run request;
// *composes.Service implements it.
type ComposeTargets interface {
	Target(ctx context.Context, composeID int64) (composes.DeployTarget, error)
}

// Builder derives the manifest from the database and Docker.
type Builder struct {
	DB       *sql.DB
	Composes ComposeTargets
	Volumes  *volumes.Resolver
	Docker   Docker
	DataDir  string
}

// scan is everything the Composes resolve to, before any selection.
type scan struct {
	volumes   []string // real names, in first-seen order
	hostPaths []HostPathCandidate
	imageRefs []string // image refs and built names
	imageIDs  []string // IDs of Containers' images
	warnings  []string
}

func (b Builder) composeIDs(ctx context.Context) ([]int64, error) {
	rows, err := b.DB.QueryContext(ctx, "SELECT id FROM composes ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (b Builder) scan(ctx context.Context) (scan, error) {
	ids, err := b.composeIDs(ctx)
	if err != nil {
		return scan{}, err
	}
	var s scan
	paths := map[string]int{}
	for _, id := range ids {
		target, err := b.Composes.Target(ctx, id)
		if err != nil {
			return scan{}, fmt.Errorf("compose %d: %w", id, err)
		}
		name, req := target.Compose.Name, target.Request
		if handWrittenRouters(target.Compose.YAMLContent) {
			s.warnings = append(s.warnings, name+": hand-written Traefik routers get no maintenance page")
		}
		if _, err := os.Stat(req.ComposeFilePath); errors.Is(err, fs.ErrNotExist) {
			s.warnings = append(s.warnings, name+" was never deployed; only its files and database rows move")
			continue
		}
		ref := volumes.ComposeRef{ProjectName: req.ProjectName, ComposeFilePath: req.ComposeFilePath, EnvFilePath: req.EnvFilePath, AllProfiles: true}
		res, err := b.Volumes.Resolve(ctx, ref)
		if err != nil {
			return scan{}, fmt.Errorf("resolve %s: %w", name, err)
		}
		for _, v := range res.Volumes {
			switch {
			case v.Kind == volumes.VolumeNamed:
				s.volumes = appendNew(s.volumes, v.Source)
			case !inside(b.DataDir, v.Source):
				i, ok := paths[v.Source]
				if !ok {
					i = len(s.hostPaths)
					paths[v.Source] = i
					s.hostPaths = append(s.hostPaths, HostPathCandidate{Path: v.Source, System: IsSystemPath(v.Source, b.DataDir)})
				}
				if !slices.Contains(s.hostPaths[i].Composes, name) {
					s.hostPaths[i].Composes = append(s.hostPaths[i].Composes, name)
				}
			}
		}
		orphans, err := b.Volumes.ListOrphans(ctx, ref)
		if err != nil {
			return scan{}, fmt.Errorf("list orphan volumes of %s: %w", name, err)
		}
		for _, o := range orphans {
			s.volumes = appendNew(s.volumes, o.Name)
		}
		refs, built, err := b.Volumes.Images(ctx, ref)
		if err != nil {
			return scan{}, fmt.Errorf("list images of %s: %w", name, err)
		}
		for _, r := range append(refs, built...) {
			s.imageRefs = appendNew(s.imageRefs, r)
		}
		containerImages, err := b.Docker.ProjectContainerImages(ctx, req.ProjectName)
		if err != nil {
			return scan{}, fmt.Errorf("list containers of %s: %w", name, err)
		}
		for _, id := range containerImages {
			s.imageIDs = appendNew(s.imageIDs, id)
		}
	}
	return s, nil
}

// HostPathCandidates lists the bind sources outside the data directory, with
// their sizes, and the warnings resolution produced.
func (b Builder) HostPathCandidates(ctx context.Context) ([]HostPathCandidate, []string, error) {
	s, err := b.scan(ctx)
	if err != nil {
		return nil, nil, err
	}
	for i := range s.hostPaths {
		if s.hostPaths[i].System {
			continue
		}
		size, err := b.Docker.Size(ctx, filepath.Dir(s.hostPaths[i].Path), filepath.Base(s.hostPaths[i].Path))
		if err != nil {
			// One unreadable path, such as a bind source deleted since the
			// deploy, must not hide every other check.
			s.warnings = append(s.warnings, fmt.Sprintf("Could not size %s: %v", s.hostPaths[i].Path, err))
			continue
		}
		s.hostPaths[i].Size = size
	}
	return s.hostPaths, s.warnings, nil
}

// Build lists every item to move. running is recorded as is; hostPaths must
// each be a candidate.
func (b Builder) Build(ctx context.Context, running []int64, hostPaths []string) (Manifest, error) {
	s, err := b.scan(ctx)
	if err != nil {
		return Manifest{}, err
	}
	version, err := database.SchemaVersion(ctx, b.DB)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{Protocol: ProtocolVersion, SchemaVersion: version, DataDir: b.DataDir,
		RunningComposeIDs: nonNilIDs(running), Items: []Item{}, Warnings: s.warnings}
	if m.Warnings == nil {
		m.Warnings = []string{}
	}

	dbSize, dirSize, err := b.dataDirSizes()
	if err != nil {
		return Manifest{}, err
	}
	m.Items = append(m.Items, Item{ID: "database", Kind: KindDatabase, Size: dbSize}, Item{ID: "data-dir", Kind: KindDataDir, Size: dirSize})

	for _, name := range s.volumes {
		v, ok, err := b.Docker.InspectVolume(ctx, name)
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect volume %s: %w", name, err)
		}
		if !ok {
			m.Warnings = append(m.Warnings, "Volume "+name+" does not exist yet; its Compose creates it on deploy")
			continue
		}
		if (v.Driver != "" && v.Driver != "local") || len(v.Options) > 0 {
			m.Warnings = append(m.Warnings, fmt.Sprintf("Volume %s uses driver %s and is not moved", name, driverName(v)))
			continue
		}
		size, err := b.Docker.Size(ctx, name, ".")
		if err != nil {
			return Manifest{}, fmt.Errorf("size volume %s: %w", name, err)
		}
		vol := v
		m.Items = append(m.Items, Item{ID: "volume-" + name, Kind: KindVolume, Size: size, Volume: &vol})
	}

	for _, p := range hostPaths {
		if !slices.ContainsFunc(s.hostPaths, func(c HostPathCandidate) bool { return c.Path == p }) {
			return Manifest{}, fmt.Errorf("%s is not a host path any Compose mounts", p)
		}
		size, err := b.Docker.Size(ctx, filepath.Dir(p), filepath.Base(p))
		if err != nil {
			return Manifest{}, fmt.Errorf("size %s: %w", p, err)
		}
		m.Items = append(m.Items, Item{ID: "host-path-" + hostPathID(p), Kind: KindHostPath, Size: size, HostPath: p})
	}

	seen := map[string]bool{}
	for _, ref := range append(slices.Clone(s.imageRefs), s.imageIDs...) {
		img, ok, err := b.Docker.InspectImage(ctx, ref)
		if err != nil {
			return Manifest{}, fmt.Errorf("inspect image %s: %w", ref, err)
		}
		if !ok {
			m.Warnings = append(m.Warnings, "Image "+ref+" is not on this host; its Compose will pull or build it on deploy")
			continue
		}
		if seen[img.ID] {
			continue
		}
		seen[img.ID] = true
		info := img
		m.Items = append(m.Items, Item{ID: "image-" + shortID(img.ID), Kind: KindImage, Size: img.Size, Image: &info})
	}
	return m, nil
}

// dataDirSizes sizes panel.db and the rest of the data directory, without
// the database files and the Migration's own directories.
func (b Builder) dataDirSizes() (dbSize, dirSize int64, err error) {
	if info, err := os.Stat(filepath.Join(b.DataDir, "panel.db")); err == nil {
		dbSize = info.Size()
	}
	err = filepath.WalkDir(b.DataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(b.DataDir, p)
		if rel != "." && skipInDataDir(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			dirSize += info.Size()
		}
		return nil
	})
	return dbSize, dirSize, err
}

// skipInDataDir reports the data directory paths the data-dir item leaves
// out: the database, which is its own item, and the Migration's scratch.
func skipInDataDir(rel string) bool {
	switch rel {
	case "panel.db", "panel.db-journal", "panel.db-wal", "panel.db-shm", ".migration", ".migration-out":
		return true
	}
	return false
}

// IsSystemPath reports a host path that is unticked by default: system
// directories, sockets, and anything that is or holds the data directory or
// Docker's own storage, which would carry the live database and Volumes.
func IsSystemPath(p, dataDir string) bool {
	if p == "/" || strings.HasSuffix(p, ".sock") {
		return true
	}
	for _, root := range []string{"/etc", "/proc", "/sys", "/dev", "/run", "/var/run", "/var/lib/docker"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	for _, held := range []string{dataDir, "/var/lib/docker"} {
		if held != "" && inside(p, held) {
			return true
		}
	}
	return inside(dataDir, p)
}

func inside(dir, p string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../")))
}

func hostPathID(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])[:12]
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func driverName(v VolumeInfo) string {
	if v.Driver == "" {
		return "local"
	}
	return v.Driver
}

func handWrittenRouters(yamlContent string) bool {
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(yamlContent), &doc) != nil {
		return false
	}
	for _, service := range doc.Services {
		if domains.HasTraefikLabels(service) {
			return true
		}
	}
	return false
}

func appendNew(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
