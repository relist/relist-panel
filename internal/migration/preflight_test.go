package migration

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"relist-panel/internal/config"
)

func TestEvaluate(t *testing.T) {
	base := func() SourceInfo {
		return SourceInfo{Protocol: ProtocolVersion, SchemaVersion: 18, DataDir: "/var/lib/relist-panel",
			Install:     config.InstallSettings{Domain: "panel.example.com"},
			HostPaths:   []HostPathCandidate{{Path: "/srv/uploads", Size: 100}},
			Volumes:     []Item{{ID: "volume-pg", Kind: KindVolume, Size: 1000, Volume: &VolumeInfo{Name: "pg"}}},
			Images:      []Item{{ID: "image-a", Kind: KindImage, Size: 500}},
			DataDirSize: 50, DatabaseSize: 10, Warnings: []string{"draft was never deployed; only its files and database rows move"}}
	}
	type row struct {
		name     string
		src      func(SourceInfo) SourceInfo
		local    func(*LocalHost, *FakeDocker, *sql.DB)
		selected []string
		want     Check
	}
	rows := []row{
		{name: "protocol", src: func(s SourceInfo) SourceInfo { s.Protocol = 2; return s },
			want: Check{true, "The two panels speak different migration protocols. Use the same panel version on both"}},
		{name: "schema", src: func(s SourceInfo) SourceInfo { s.SchemaVersion = 19; return s },
			want: Check{true, "This panel is older than the source. Upgrade it first"}},
		{name: "data dir", src: func(s SourceInfo) SourceInfo { s.DataDir = "/srv/relist"; return s },
			want: Check{true, "RELIST_DATA_DIR differs: set it to /srv/relist on this host"}},
		{name: "not empty", local: func(_ *LocalHost, _ *FakeDocker, db *sql.DB) {
			db.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('p', 1, 1)`)
		}, want: Check{true, "This panel already has data. Migrate into a freshly installed panel"}},
		{name: "volume exists", local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.Volumes["pg"] = VolumeInfo{Name: "pg"} },
			want: Check{true, "Volume pg already exists on this host"}},
		{name: "host path busy", selected: []string{"/srv/uploads"},
			local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.Sizes["/srv|uploads"] = 7 },
			want:  Check{true, "/srv/uploads exists on this host and is not empty"}},
		{name: "data dir space", local: func(l *LocalHost, _ *FakeDocker, _ *sql.DB) {
			l.FreeBytes = func(string) (int64, error) { return 10, nil }
		}, want: Check{true, "Not enough free space in /var/lib/relist-panel: need 66 B, have 10 B"}},
		{name: "docker root space", local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.FreeB["/var/lib/docker"] = 1000 },
			want: Check{true, "Not enough free space in /var/lib/docker: need 1.6 KiB, have 1000 B"}},
		{name: "install differs", local: func(l *LocalHost, _ *FakeDocker, _ *sql.DB) { l.Install.Domain = "panel.new.example.com" },
			want: Check{false, "RELIST_DOMAIN is panel.example.com on the source and panel.new.example.com here; copy it into this host's .env"}},
		{name: "source warning", want: Check{false, "draft was never deployed; only its files and database rows move"}},
		{name: "active deployment", src: func(s SourceInfo) SourceInfo { s.ActiveDeployment = "api #4"; return s },
			want: Check{false, "api #4 is running on the source; queued Deployments there will be canceled"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			db := openDB(t)
			docker := NewFakeDocker()
			local := LocalHost{DB: db, Docker: docker, DataDir: "/var/lib/relist-panel",
				Install: config.InstallSettings{Domain: "panel.example.com"}, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }}
			if r.local != nil {
				r.local(&local, docker, db)
			}
			src := base()
			if r.src != nil {
				src = r.src(src)
			}
			checks, err := Evaluate(context.Background(), local, src, r.selected)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(checks, r.want) {
				t.Fatalf("checks = %+v\nwant %+v", checks, r.want)
			}
		})
	}

	t.Run("clean", func(t *testing.T) {
		local := LocalHost{DB: openDB(t), Docker: NewFakeDocker(), DataDir: "/var/lib/relist-panel",
			Install: config.InstallSettings{Domain: "panel.example.com"}, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }}
		checks, err := Evaluate(context.Background(), local, base(), []string{"/srv/uploads"})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range checks {
			if c.Blocking {
				t.Fatalf("unexpected blocking check %q", c.Message)
			}
		}
	})
}
