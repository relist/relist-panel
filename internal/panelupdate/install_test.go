package panelupdate

import (
	"context"
	"errors"
	"testing"
)

type fakeSelf struct {
	self Self
	err  error
}

func (f fakeSelf) InspectSelf(context.Context) (Self, error) { return f.self, f.err }

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
