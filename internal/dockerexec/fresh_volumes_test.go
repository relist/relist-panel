package dockerexec

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/composes"
)

func TestTrackedRemovalChecksExistenceAndPartialDownFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		existed   bool
		removed   bool
		downFails bool
	}{
		{"named volume removed", true, true, false},
		{"volume absent before down", false, false, false},
		{"partial failed down", true, true, true},
		{"failed down preserved volume", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := command
			defer func() { command = old }()
			down := false
			command = func(ctx context.Context, args ...string) *exec.Cmd {
				if slices.Contains(args, "config") {
					return exec.CommandContext(ctx, "printf", "%s", `{"volumes":{"data":{"name":"shared-name"},"external":{"name":"keep","external":true}}}`)
				}
				if slices.Contains(args, "down") {
					down = true
					if tc.downFails {
						return exec.CommandContext(ctx, "sh", "-c", "echo volume-in-use >&2; exit 1")
					}
					return exec.CommandContext(ctx, "true")
				}
				if !slices.Contains(args, "shared-name") || slices.Contains(args, "keep") {
					t.Errorf("unexpected inspected volume args: %v", args)
				}
				if !tc.existed || (down && tc.removed) {
					return exec.CommandContext(ctx, "sh", "-c", "echo 'no such volume' >&2; exit 1")
				}
				return exec.CommandContext(ctx, "true")
			}
			removed, err := (Runner{}).DownWithVolumesTracked(context.Background(), composes.RunRequest{ProjectName: "p"})
			if removed != tc.removed || (err != nil) != tc.downFails {
				t.Fatalf("removed=%v err=%v, want %v error=%v", removed, err, tc.removed, tc.downFails)
			}
			if tc.downFails && !strings.Contains(err.Error(), "volume-in-use") {
				t.Fatalf("original down error lost: %v", err)
			}
		})
	}
}

func TestTrackedRemovalNeverTreatsUnavailableDaemonAsDeletion(t *testing.T) {
	old := command
	defer func() { command = old }()
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		if slices.Contains(args, "config") {
			return exec.CommandContext(ctx, "printf", "%s", `{"volumes":{"data":{"name":"p_data"}}}`)
		}
		return exec.CommandContext(ctx, "sh", "-c", "echo 'cannot connect to Docker daemon' >&2; exit 1")
	}
	removed, err := (Runner{}).DownWithVolumesTracked(context.Background(), composes.RunRequest{})
	if removed || err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}
