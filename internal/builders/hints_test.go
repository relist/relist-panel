package builders

import (
	"context"
	"errors"
	"testing"
)

func TestOOMHint(t *testing.T) {
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	want := "The build ran out of memory on Builder large (4 GB). Raise its memory or pick a larger Builder for this Compose."
	for _, msg := range []string{
		`process "/bin/sh -c npm ci" did not complete successfully: cannot allocate memory`,
		`process "/bin/sh -c make" did not complete successfully: exit code: 137`,
	} {
		if got := f.svc.FailureHint(context.Background(), b.ID, errors.New(msg)); got != want {
			t.Errorf("FailureHint(%q) = %q", msg, got)
		}
	}
	if got := f.svc.FailureHint(context.Background(), b.ID, errors.New("exit code: 1")); got != "" {
		t.Errorf("FailureHint(other) = %q", got)
	}
}

func TestLocalImageHint(t *testing.T) {
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	err := errors.New("failed to solve: relist-exp-base:local: failed to resolve source metadata for docker.io/library/relist-exp-base:local: pull access denied, repository does not exist")
	if got := f.svc.FailureHint(context.Background(), b.ID, err); got != "" {
		t.Fatalf("hint without the local image = %q", got)
	}
	f.engine.images["docker.io/library/relist-exp-base:local"] = true
	want := "relist-exp-base:local exists on this host, but Builders cannot use local images. Pick Docker default for this Compose, or push the image to a registry."
	if got := f.svc.FailureHint(context.Background(), b.ID, err); got != want {
		t.Fatalf("hint = %q", got)
	}
}

func TestNoHintForDockerDefault(t *testing.T) {
	f := newServiceFixture(t)
	if got := f.svc.FailureHint(context.Background(), DockerDefault, errors.New("cannot allocate memory")); got != "" {
		t.Fatalf("hint = %q", got)
	}
}
