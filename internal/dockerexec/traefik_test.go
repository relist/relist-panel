package dockerexec

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/accesslog"
	"relist-panel/internal/traefikconfig"
)

const (
	projectArgv = "docker inspect --format {{index .Config.Labels \"com.docker.compose.project\"}} panelhost"
	traefikArgv = "docker ps -aq --filter label=com.docker.compose.project=relist --filter label=com.docker.compose.service=traefik"
)

// traefikRecorder answers like recorder, can fail a call, and writes a
// separate stderr answer.
type traefikRecorder struct {
	recorder
	stderr map[string]string
	fail   map[string]error
}

func (r *traefikRecorder) run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	key := strings.Join(argv, " ")
	if err, ok := r.fail[key]; ok {
		r.calls = append(r.calls, argv)
		return err
	}
	if out, ok := r.stderr[key]; ok && stderr != nil {
		io.WriteString(stderr, out)
	}
	return r.recorder.run(ctx, argv, stdin, stdout, stderr)
}

func newTestTraefikDocker(answers map[string]string) (*TraefikDocker, *traefikRecorder) {
	r := &traefikRecorder{recorder: recorder{answers: answers}}
	return &TraefikDocker{runDocker: r.run, hostname: func() string { return "panelhost" }}, r
}

func foundAnswers() map[string]string {
	return map[string]string{projectArgv: "relist\n", traefikArgv: "abc123\n"}
}

func TestTraefikReopenArgv(t *testing.T) {
	d, r := newTestTraefikDocker(foundAnswers())
	if err := d.Reopen(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "kill", "--signal=USR1", "abc123"}
	if got := r.calls[len(r.calls)-1]; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestTraefikLogsArgvAndStderr(t *testing.T) {
	answers := foundAnswers()
	logsArgv := "docker logs --tail 500 --timestamps abc123"
	answers[logsArgv] = "2026-11-03T07:32:27Z INF out\n"
	d, r := newTestTraefikDocker(answers)
	r.stderr = map[string]string{logsArgv: "2026-11-03T07:32:28Z ERR err\n"}
	got, err := d.Logs(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "INF out") || !strings.Contains(got, "ERR err") {
		t.Fatalf("logs = %q, want stdout and stderr", got)
	}
}

func TestTraefikArgs(t *testing.T) {
	answers := foundAnswers()
	answers["docker inspect --format {{range .Args}}{{println .}}{{end}} abc123"] = "--log.level=INFO\n--accesslog=true\n\n"
	d, _ := newTestTraefikDocker(answers)
	got, err := d.Args(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--log.level=INFO", "--accesslog=true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestTraefikRestartArgv(t *testing.T) {
	d, r := newTestTraefikDocker(foundAnswers())
	if err := d.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"docker", "restart", "-t", "10", "abc123"}; !reflect.DeepEqual(r.calls[len(r.calls)-1], want) {
		t.Fatalf("argv = %q, want %q", r.calls[len(r.calls)-1], want)
	}
}

func TestTraefikLogsSinceArgv(t *testing.T) {
	d, r := newTestTraefikDocker(foundAnswers())
	since := time.Date(2026, 11, 4, 1, 2, 3, 4, time.UTC)
	if _, err := d.LogsSince(context.Background(), since, 500); err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "logs", "--since", "2026-11-04T01:02:03.000000004Z", "--tail", "500", "--timestamps", "abc123"}
	if got := r.calls[len(r.calls)-1]; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestTraefikState(t *testing.T) {
	answers := foundAnswers()
	answers["docker inspect --format {{json .}} abc123"] = `{"Image":"sha256:img","RestartCount":2,
		"State":{"Running":true,"StartedAt":"2026-11-04T01:02:03.5Z"},
		"NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2"},"relist-network":{"IPAddress":"172.19.0.4"}}}}`
	d, _ := newTestTraefikDocker(answers)
	st, err := d.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := traefikconfig.State{Running: true, RestartCount: 2, StartedAt: time.Date(2026, 11, 4, 1, 2, 3, 5e8, time.UTC), Image: "sha256:img", IP: "172.19.0.4"}
	if st != want {
		t.Fatalf("state = %+v, want %+v", st, want)
	}
}

func TestTraefikDryRunOrder(t *testing.T) {
	answers := map[string]string{
		"docker inspect --format {{.State.Running}} relist-traefik-dryrun-x1": "false\n",
		"docker logs relist-traefik-dryrun-x1":                                "Command error\n",
	}
	d, r := newTestTraefikDocker(answers)
	res, err := d.DryRun(context.Background(), "x1", "/d/.dryrun-x1", "sha256:img", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Running || !strings.Contains(res.Log, "Command error") {
		t.Fatalf("result = %+v", res)
	}
	var verbs []string
	for _, c := range r.calls {
		verbs = append(verbs, c[1])
	}
	if want := []string{"run", "inspect", "logs", "rm"}; !reflect.DeepEqual(verbs, want) {
		t.Fatalf("calls = %q", r.calls)
	}
	wantRun := []string{"docker", "run", "-d", "--name", "relist-traefik-dryrun-x1", "--network", "none", "--tmpfs", "/letsencrypt",
		"-v", "/d/.dryrun-x1:/etc/traefik:ro", "sha256:img"}
	if !reflect.DeepEqual(r.calls[0], wantRun) {
		t.Fatalf("run argv = %q", r.calls[0])
	}
	if want := []string{"docker", "rm", "-f", "relist-traefik-dryrun-x1"}; !reflect.DeepEqual(r.calls[3], want) {
		t.Fatalf("rm argv = %q", r.calls[3])
	}
}

func TestTraefikDryRunRemovesAfterFailedRun(t *testing.T) {
	d, r := newTestTraefikDocker(nil)
	runKey := "docker run -d --name relist-traefik-dryrun-x2 --network none --tmpfs /letsencrypt -v /d:/etc/traefik:ro img"
	r.fail = map[string]error{runKey: errors.New("exit status 125")}
	if _, err := d.DryRun(context.Background(), "x2", "/d", "img", 0); err == nil {
		t.Fatal("no error from a failed run")
	}
	if last := r.calls[len(r.calls)-1]; strings.Join(last, " ") != "docker rm -f relist-traefik-dryrun-x2" {
		t.Fatalf("last call = %q", last)
	}
}

func TestTraefikRemoveDryRuns(t *testing.T) {
	answers := map[string]string{"docker ps -aq --filter name=relist-traefik-dryrun-": "a1\nb2\n"}
	d, r := newTestTraefikDocker(answers)
	if err := d.RemoveDryRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last := strings.Join(r.calls[len(r.calls)-1], " "); last != "docker rm -f a1 b2" {
		t.Fatalf("last call = %q", last)
	}
	d, r = newTestTraefikDocker(nil)
	if err := d.RemoveDryRuns(context.Background()); err != nil || len(r.calls) != 1 {
		t.Fatalf("with none: err = %v, calls = %q", err, r.calls)
	}
}

func TestTraefikNotFound(t *testing.T) {
	cases := map[string]func() *TraefikDocker{
		"no traefik service": func() *TraefikDocker {
			d, _ := newTestTraefikDocker(map[string]string{projectArgv: "relist\n"})
			return d
		},
		"no project label": func() *TraefikDocker {
			d, _ := newTestTraefikDocker(map[string]string{projectArgv: "\n"})
			return d
		},
		"not in a container": func() *TraefikDocker {
			d, r := newTestTraefikDocker(nil)
			r.fail = map[string]error{projectArgv: errors.New("exit status 1")}
			return d
		},
	}
	for name, build := range cases {
		d := build()
		ctx := context.Background()
		_, logsErr := d.Logs(ctx, 500)
		_, argsErr := d.Args(ctx)
		_, stateErr := d.State(ctx)
		for _, err := range []error{d.Reopen(ctx), logsErr, argsErr, stateErr, d.Restart(ctx)} {
			if !errors.Is(err, accesslog.ErrNoTraefik) {
				t.Errorf("%s: error = %v, want ErrNoTraefik", name, err)
			}
		}
	}
}
