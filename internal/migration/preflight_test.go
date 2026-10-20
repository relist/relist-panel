package migration

import (
	"context"
	"database/sql"
	"errors"
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
		}, want: Check{true, "This panel already has Projects. Migrate into a freshly installed panel"}},
		{name: "volume exists", local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.Volumes["pg"] = VolumeInfo{Name: "pg"} },
			want: Check{true, "Volume pg already exists on this host"}},
		{name: "volume with a driver", src: func(s SourceInfo) SourceInfo {
			s.Volumes = append(s.Volumes, Item{ID: "volume-nfs", Kind: KindVolume, Volume: &VolumeInfo{Name: "nfs", Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": "/"}}})
			return s
		}, want: Check{true, "Volume nfs has driver options (device=/, o=bind, type=none) and cannot be restored here"}},
		{name: "host path busy", selected: []string{"/srv/uploads"},
			local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.Paths["/srv|uploads"] = PathOccupied },
			want:  Check{true, "/srv/uploads exists on this host and is not empty"}},
		{name: "host path holds only empty files", selected: []string{"/srv/uploads"},
			local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) {
				d.Paths["/srv|uploads"] = PathOccupied
				d.Sizes["/srv|uploads"] = 0
			},
			want: Check{true, "/srv/uploads exists on this host and is not empty"}},
		{name: "host path holds this panel's data", selected: []string{"/var/lib/relist-panel/x"},
			src: func(s SourceInfo) SourceInfo {
				s.HostPaths = append(s.HostPaths, HostPathCandidate{Path: "/var/lib/relist-panel/x"})
				return s
			},
			want: Check{true, "/var/lib/relist-panel/x holds this panel's data or Docker's storage and cannot be restored here"}},
		{name: "host path is the root", selected: []string{"/"},
			src: func(s SourceInfo) SourceInfo {
				s.HostPaths = append(s.HostPaths, HostPathCandidate{Path: "/"})
				return s
			},
			want: Check{true, "/ holds this panel's data or Docker's storage and cannot be restored here"}},
		{name: "host path unreadable", selected: []string{"/srv/uploads"},
			local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.Fail["probe"] = errors.New("docker is down") },
			want:  Check{true, "Could not check /srv/uploads on this host: docker is down"}},
		{name: "data dir space", local: func(l *LocalHost, _ *FakeDocker, _ *sql.DB) {
			l.FreeBytes = func(string) (int64, error) { return 10, nil }
		}, want: Check{true, "Not enough free space in /var/lib/relist-panel: need 66 B, have 10 B"}},
		{name: "docker root space", local: func(_ *LocalHost, d *FakeDocker, _ *sql.DB) { d.FreeB["/var/lib/docker"] = 1000 },
			want: Check{true, "Not enough free space in /var/lib/docker: need 1.6 KiB, have 1000 B"}},
		{name: "install differs", local: func(l *LocalHost, _ *FakeDocker, _ *sql.DB) { l.Install.Domain = "panel.new.example.com" },
			want: Check{false, "RELIST_DOMAIN is panel.example.com on the source and panel.new.example.com here; copy it into this host's .env"}},
		{name: "source warning", want: Check{false, "draft was never deployed; only its files and database rows move"}},
		{name: "queued deployments", src: func(s SourceInfo) SourceInfo { s.QueuedDeployments = 2; return s },
			want: Check{false, "2 queued Deployments on the source will be canceled when the migration starts"}},
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

	t.Run("an admin seeded from the env is not data", func(t *testing.T) {
		db := openDB(t)
		if _, err := db.Exec(`INSERT INTO admin_credentials (id, email, password_hash, created_at) VALUES (1, 'a@example.com', 'x', 1)`); err != nil {
			t.Fatal(err)
		}
		local := LocalHost{DB: db, Docker: NewFakeDocker(), DataDir: "/var/lib/relist-panel",
			Install: config.InstallSettings{Domain: "panel.example.com"}, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }}
		checks, err := Evaluate(context.Background(), local, base(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range checks {
			if c.Blocking {
				t.Fatalf("an admin-only panel was blocked: %q", c.Message)
			}
		}
	})

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
