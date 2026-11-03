package dockerexec

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/accesslog"
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
		for _, err := range []error{d.Reopen(ctx), logsErr, argsErr} {
			if !errors.Is(err, accesslog.ErrNoTraefik) {
				t.Errorf("%s: error = %v, want ErrNoTraefik", name, err)
			}
		}
	}
}
