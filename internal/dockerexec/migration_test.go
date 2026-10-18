package dockerexec

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/composes"
)

// recorder captures every argv a MigrationDocker runs and answers with canned
// stdout keyed by the argv joined with spaces.
type recorder struct {
	calls   [][]string
	answers map[string]string
}

func (r *recorder) run(_ context.Context, argv []string, _ io.Reader, stdout, _ io.Writer) error {
	r.calls = append(r.calls, argv)
	if out, ok := r.answers[strings.Join(argv, " ")]; ok && stdout != nil {
		io.WriteString(stdout, out)
	}
	return nil
}

func newTestMigrationDocker(r *recorder) *MigrationDocker {
	return &MigrationDocker{
		runDocker:    r.run,
		resolveImage: func(context.Context) (string, error) { return testImage, nil },
		hostname:     func() string { return "panelhost" },
	}
}

func TestPackArgvIsReadOnlyWithDacReadSearch(t *testing.T) {
	r := &recorder{}
	if err := newTestMigrationDocker(r).Pack(context.Background(), "relist-c7_pgdata", ".", io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "run", "--rm", "-i", "--log-driver", "none", "--network", "none", "--user", "0:0", "--read-only", "--tmpfs", "/tmp",
		"--cap-drop", "ALL", "--cap-add", "DAC_READ_SEARCH", "--security-opt", "no-new-privileges",
		"-v", "relist-c7_pgdata:/data:ro", "--entrypoint", helperEntrypoint, testImage, "pack", "--", "."}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("argv = %q\nwant  %q", r.calls[0], want)
	}
}

func TestUnpackArgvAddsChownCaps(t *testing.T) {
	r := &recorder{}
	if err := newTestMigrationDocker(r).Unpack(context.Background(), "/srv", strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "run", "--rm", "-i", "--log-driver", "none", "--network", "none", "--user", "0:0", "--read-only", "--tmpfs", "/tmp",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "-v", "/srv:/data", "--entrypoint", helperEntrypoint, testImage, "unpack", "--", "."}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("argv = %q\nwant  %q", r.calls[0], want)
	}
}

func TestMaintenanceArgvCarriesLabels(t *testing.T) {
	r := &recorder{}
	labels := map[string]string{"traefik.enable": "true", "b": "2"}
	if err := newTestMigrationDocker(r).RunMaintenance(context.Background(), labels); err != nil {
		t.Fatal(err)
	}
	if got := r.calls[0]; !reflect.DeepEqual(got, []string{"docker", "rm", "-f", "relist-maintenance"}) {
		t.Fatalf("first argv = %q, want the old container removed", got)
	}
	want := []string{"docker", "run", "-d", "--name", "relist-maintenance", "--restart", "unless-stopped",
		"--network", "relist-network", "--label", "relist.maintenance=1",
		"--label", "b=2", "--label", "traefik.enable=true",
		"--entrypoint", "relist-panel", testImage, "maintenance-page"}
	if !reflect.DeepEqual(r.calls[1], want) {
		t.Fatalf("argv = %q\nwant  %q", r.calls[1], want)
	}
}

func TestComposeStopArgv(t *testing.T) {
	r := &recorder{}
	req := composes.RunRequest{ProjectName: "relist-c7", ComposeFilePath: "/d/c/docker-compose.yml", EnvFilePath: "/d/c/.env"}
	if err := newTestMigrationDocker(r).ComposeStop(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "compose", "-p", "relist-c7", "-f", "/d/c/docker-compose.yml", "--env-file", "/d/c/.env", "stop"}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("argv = %q\nwant  %q", r.calls[0], want)
	}
}

func TestInspectImageParsesTagsAndDigests(t *testing.T) {
	r := &recorder{answers: map[string]string{
		"docker image inspect --format {{json .}} redis:7": `{"Id":"sha256:abc","RepoTags":["redis:7"],"RepoDigests":["redis@sha256:def"],"Size":42}`,
	}}
	got, ok, err := newTestMigrationDocker(r).InspectImage(context.Background(), "redis:7")
	if err != nil || !ok {
		t.Fatalf("InspectImage() = %v, %v", ok, err)
	}
	if got.ID != "sha256:abc" || got.Tags[0] != "redis:7" || got.RepoDigests[0] != "redis@sha256:def" || got.Size != 42 {
		t.Fatalf("InspectImage() = %+v", got)
	}
}

func TestStopTraefikFindsTheInstallsTraefik(t *testing.T) {
	r := &recorder{answers: map[string]string{
		`docker inspect --format {{index .Config.Labels "com.docker.compose.project"}} panelhost`:                          "relist\n",
		"docker ps -aq --filter label=com.docker.compose.project=relist --filter label=com.docker.compose.service=traefik": "f00d\n",
	}}
	if err := newTestMigrationDocker(r).StopTraefik(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.calls[len(r.calls)-1]; !reflect.DeepEqual(got, []string{"docker", "stop", "f00d"}) {
		t.Fatalf("last argv = %q", got)
	}
}
