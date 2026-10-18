package migration

import (
	"context"
	"fmt"

	"relist-panel/internal/config"
	"relist-panel/internal/database"
)

// SourceInfo is what the old host tells the new one before anything is
// locked, so every blocking check runs first.
type SourceInfo struct {
	Protocol         int                    `json:"protocol"`
	SchemaVersion    int64                  `json:"schemaVersion"`
	DataDir          string                 `json:"dataDir"`
	Install          config.InstallSettings `json:"install"`
	HostPaths        []HostPathCandidate    `json:"hostPaths"`
	Volumes          []Item                 `json:"volumes"`
	Images           []Item                 `json:"images"`
	DataDirSize      int64                  `json:"dataDirSize"`
	DatabaseSize     int64                  `json:"databaseSize"`
	ActiveDeployment string                 `json:"activeDeployment,omitempty"`
	Warnings         []string               `json:"warnings"`
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
