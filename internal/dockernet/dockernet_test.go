package dockernet_test

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"testing"

	"relist-panel/internal/dockernet"
)

func TestEnsureNetworkCreatesMissingNetwork(t *testing.T) {
	requireDocker(t)
	name := testNetworkName(t)

	if err := dockernet.EnsureNetwork(context.Background(), name); err != nil {
		t.Fatalf("EnsureNetwork() error = %v", err)
	}

	if !networkExists(t, name) {
		t.Fatalf("network %q was not created", name)
	}
}

func TestEnsureNetworkIsIdempotent(t *testing.T) {
	requireDocker(t)
	name := testNetworkName(t)

	if err := dockernet.EnsureNetwork(context.Background(), name); err != nil {
		t.Fatalf("first EnsureNetwork() error = %v", err)
	}
	if err := dockernet.EnsureNetwork(context.Background(), name); err != nil {
		t.Fatalf("second EnsureNetwork() error = %v", err)
	}
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
}

func testNetworkName(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("relist-panel-test-%d", rand.Int63())
	t.Cleanup(func() {
		_ = exec.Command("docker", "network", "rm", name).Run()
	})
	return name
}

func networkExists(t *testing.T, name string) bool {
	t.Helper()
	return exec.Command("docker", "network", "inspect", name).Run() == nil
}
