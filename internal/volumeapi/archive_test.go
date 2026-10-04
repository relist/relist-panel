package volumeapi

import (
	"testing"

	"relist-panel/internal/volumes"
)

// archiveName names the download: after the Volume for a whole-Volume
// archive — its logical name, or the base of a bind's resolved source — and
// after the archived path's base otherwise.
func TestArchiveNameNamesTheVolumeOrTheArchivedPath(t *testing.T) {
	named := volumes.Volume{Kind: volumes.VolumeNamed, LogicalName: "pgdata", Source: "demoapp_pgdata"}
	bind := volumes.Volume{Kind: volumes.VolumeBind, Source: "/data/app/data"}

	tests := []struct {
		name   string
		volume volumes.Volume
		path   string
		want   string
	}{
		{name: "the whole named volume takes its logical name", volume: named, path: ".", want: "pgdata.tar.gz"},
		{name: "the whole bind takes its source's base", volume: bind, path: ".", want: "data.tar.gz"},
		{name: "a directory takes its base", volume: named, path: "backups", want: "backups.tar.gz"},
		{name: "a trailing slash does not change the base", volume: named, path: "backups/", want: "backups.tar.gz"},
		{name: "a nested path takes its base", volume: bind, path: "sub/file.bin", want: "file.bin.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := archiveName(tt.volume, tt.path); got != tt.want {
				t.Errorf("archiveName(%+v, %q) = %q, want %q", tt.volume, tt.path, got, tt.want)
			}
		})
	}
}
