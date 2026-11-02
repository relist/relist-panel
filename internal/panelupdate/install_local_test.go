package panelupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeFinder struct {
	selves []Self
	err    error
	gotDir string
}

func (f *fakeFinder) ContainersInDir(_ context.Context, dir string) ([]Self, error) {
	f.gotDir = dir
	return f.selves, f.err
}

func TestReadLocalFilesRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker-compose.yml")
	os.WriteFile(compose, []byte("services: {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "real.env"), []byte("A=1\n"), 0o600)
	env := filepath.Join(dir, ".env")
	if err := os.Symlink("real.env", env); err != nil {
		t.Fatal(err)
	}
	_, err := ReadLocalFiles(Install{Dir: dir, ConfigFiles: []string{compose}})
	var un *Unavailable
	if !errors.As(err, &un) || !strings.Contains(un.Reason, env) {
		t.Fatalf("err = %v, want *Unavailable naming %s", err, env)
	}
}

func TestReadLocalFilesMissingEnvIsEmpty(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker-compose.yml")
	os.WriteFile(compose, []byte("services: {}\n"), 0o644)
	got, err := ReadLocalFiles(Install{Dir: dir, ConfigFiles: []string{compose}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Env) != 0 || len(got.Config) != 1 || string(got.Config[0]) != "services: {}\n" {
		t.Errorf("got %+v", got)
	}
}

func TestReadLocalFilesMissingConfigIsError(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadLocalFiles(Install{Dir: dir, ConfigFiles: []string{filepath.Join(dir, "docker-compose.yml")}})
	if err == nil {
		t.Fatal("want an error")
	}
}

func TestResolveInstallFromArgs(t *testing.T) {
	f := &fakeFinder{}
	in, err := ResolveInstall(context.Background(), InstallArgs{Project: "p", Dir: "/opt/x", Files: []string{"/opt/x/docker-compose.yml"}}, f)
	if err != nil {
		t.Fatal(err)
	}
	want := Install{Project: "p", Dir: "/opt/x", ConfigFiles: []string{"/opt/x/docker-compose.yml"}}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("got %+v", in)
	}
	if f.gotDir != "" {
		t.Error("finder should not be asked")
	}
	_, err = ResolveInstall(context.Background(), InstallArgs{Project: "p", Dir: "/opt/x", Files: []string{"/etc/other.yml"}}, f)
	var un *Unavailable
	if !errors.As(err, &un) {
		t.Errorf("file outside dir: err = %v", err)
	}
}

func TestResolveInstallFromLabelsInDir(t *testing.T) {
	f := &fakeFinder{selves: []Self{{ImageID: "sha256:a", Labels: map[string]string{
		labelProject:     "relist-panel",
		labelWorkingDir:  "/opt/x",
		labelConfigFiles: "/opt/x/docker-compose.yml,/opt/x/docker-compose.override.yml",
	}}}}
	in, err := ResolveInstall(context.Background(), InstallArgs{Dir: "/opt/x"}, f)
	if err != nil {
		t.Fatal(err)
	}
	if in.Project != "relist-panel" || len(in.ConfigFiles) != 2 || in.Image != "sha256:a" {
		t.Errorf("got %+v", in)
	}
	if f.gotDir != "/opt/x" {
		t.Errorf("asked for %q", f.gotDir)
	}
}

func TestResolveInstallFallbackUsesComposeProjectName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "install")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("COMPOSE_PROJECT_NAME=relist-panel\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "docker-compose.override.yml"), []byte("services: {}\n"), 0o644)
	in, err := ResolveInstall(context.Background(), InstallArgs{Dir: dir}, &fakeFinder{})
	if err != nil {
		t.Fatal(err)
	}
	want := Install{Project: "relist-panel", Dir: dir, ConfigFiles: []string{
		filepath.Join(dir, "docker-compose.yml"), filepath.Join(dir, "docker-compose.override.yml")}}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("got %+v, want %+v", in, want)
	}
}

func TestResolveInstallFallbackUsesDirName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myinstall")
	os.MkdirAll(dir, 0o755)
	in, err := ResolveInstall(context.Background(), InstallArgs{Dir: dir}, &fakeFinder{})
	if err != nil {
		t.Fatal(err)
	}
	if in.Project != "myinstall" || len(in.ConfigFiles) != 1 {
		t.Errorf("got %+v", in)
	}
}

func TestResolveInstallNeedsAbsoluteDir(t *testing.T) {
	_, err := ResolveInstall(context.Background(), InstallArgs{Dir: "rel"}, &fakeFinder{})
	if err == nil {
		t.Fatal("want an error")
	}
}
