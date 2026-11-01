package panelupdate

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type fakeSelf struct {
	self Self
	err  error
}

func (f fakeSelf) InspectSelf(context.Context) (Self, error) { return f.self, f.err }

// fakeHelper serves files by path and speaks readScript's exit protocol.
type fakeHelper struct {
	files map[string]string
	runs  []HelperRun
	err   error
}

func (f *fakeHelper) Run(_ context.Context, run HelperRun, stdout io.Writer) error {
	f.runs = append(f.runs, run)
	if f.err != nil {
		return f.err
	}
	data, ok := f.files[run.Argv[len(run.Argv)-1]]
	if !ok {
		return &HelperExitError{Code: missingExit}
	}
	_, _ = io.WriteString(stdout, data)
	return nil
}

func composeLabels() map[string]string {
	return map[string]string{
		labelProject:     "relist-panel",
		labelWorkingDir:  "/srv/relist",
		labelConfigFiles: "/srv/relist/docker-compose.yml, /srv/relist/docker-compose.override.yml",
	}
}

func TestFindInstall(t *testing.T) {
	cases := []struct {
		name   string
		self   fakeSelf
		reason string
	}{
		{"compose", fakeSelf{self: Self{ImageID: "sha256:1", Labels: composeLabels()}}, ""},
		{"plain docker run", fakeSelf{self: Self{}}, reasonNotCompose},
		{"no config files", fakeSelf{self: Self{Labels: map[string]string{labelProject: "p", labelWorkingDir: "/srv"}}}, reasonNotCompose},
		{"cannot inspect", fakeSelf{err: errors.New("no socket")}, reasonNoInspect},
		{"relative dir", fakeSelf{self: Self{Labels: map[string]string{labelProject: "p", labelWorkingDir: "x", labelConfigFiles: "/x/a.yml"}}}, reasonRelativeDir},
		{"file outside dir", fakeSelf{self: Self{Labels: map[string]string{labelProject: "p", labelWorkingDir: "/srv/a", labelConfigFiles: "/srv/a/../b/c.yml"}}}, reasonOutsideDir},
		{"sibling prefix", fakeSelf{self: Self{Labels: map[string]string{labelProject: "p", labelWorkingDir: "/srv/a", labelConfigFiles: "/srv/ab/c.yml"}}}, reasonOutsideDir},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(Deps{Self: c.self})
			in, err := s.FindInstall(context.Background())
			if c.reason == "" {
				if err != nil || in.Project != "relist-panel" || in.Dir != "/srv/relist" || in.Image != "sha256:1" ||
					len(in.ConfigFiles) != 2 || in.ConfigFiles[1] != "/srv/relist/docker-compose.override.yml" {
					t.Fatalf("install = %+v, %v", in, err)
				}
				return
			}
			var u *Unavailable
			if !errors.As(err, &u) || u.Reason != c.reason {
				t.Fatalf("err = %v, want %q", err, c.reason)
			}
		})
	}
	if _, err := New(Deps{}).FindInstall(context.Background()); err == nil {
		t.Fatal("no inspector must be unavailable")
	}
}

func TestReadInstallFiles(t *testing.T) {
	in := Install{Project: "p", Dir: "/srv/relist", Image: "sha256:1",
		ConfigFiles: []string{"/srv/relist/docker-compose.yml", "/srv/relist/docker-compose.override.yml"}}
	h := &fakeHelper{files: map[string]string{
		"/srv/relist/docker-compose.yml":          "base",
		"/srv/relist/docker-compose.override.yml": "over",
		"/srv/relist/.env":                        "A=1\n",
	}}
	s := New(Deps{Helper: h})
	got, err := s.ReadInstallFiles(context.Background(), in)
	if err != nil || string(got.Config[0]) != "base" || string(got.Config[1]) != "over" || string(got.Env) != "A=1\n" {
		t.Fatalf("files = %+v, %v", got, err)
	}
	for _, run := range h.runs {
		if run.Image != "sha256:1" || run.Dir != "/srv/relist" {
			t.Fatalf("run = %+v", run)
		}
	}

	t.Run("missing .env is empty", func(t *testing.T) {
		delete(h.files, "/srv/relist/.env")
		got, err := s.ReadInstallFiles(context.Background(), in)
		if err != nil || len(got.Env) != 0 || len(got.Config) != 2 {
			t.Fatalf("files = %+v, %v", got, err)
		}
	})
	t.Run("missing config file fails", func(t *testing.T) {
		delete(h.files, "/srv/relist/docker-compose.override.yml")
		if _, err := s.ReadInstallFiles(context.Background(), in); err == nil || !strings.Contains(err.Error(), "docker-compose.override.yml") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("helper failure fails", func(t *testing.T) {
		bad := New(Deps{Helper: &fakeHelper{err: &HelperExitError{Code: 1, Stderr: "denied"}}})
		if _, err := bad.ReadInstallFiles(context.Background(), in); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("no helper is unavailable", func(t *testing.T) {
		var u *Unavailable
		if _, err := New(Deps{}).ReadInstallFiles(context.Background(), in); !errors.As(err, &u) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestStatusReportsInstall(t *testing.T) {
	s, _ := newService(t, "1.3.0")
	_, st := serve(t, s, "GET", "/api/panel-update/status", "")
	if st.Install.Available || st.Install.Reason != reasonNoInspect {
		t.Fatalf("install = %+v", st.Install)
	}
	s.d.Self = fakeSelf{self: Self{Labels: composeLabels()}}
	_, st = serve(t, s, "GET", "/api/panel-update/status", "")
	if !st.Install.Available || st.Install.Reason != "" {
		t.Fatalf("install = %+v", st.Install)
	}
}
