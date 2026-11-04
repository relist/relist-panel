package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRunTraefikConfigExitCodes(t *testing.T) {
	env := func(dir string) func(string) string {
		return func(k string) string {
			if k == "RELIST_DATA_DIR" {
				return dir
			}
			return ""
		}
	}
	cases := []struct {
		name string
		ops  traefikConfigOps
		args []string
		want int
	}{
		{"ok", traefikConfigOps{getenv: env("/d"), bootstrap: func(context.Context, string) error { return nil }}, nil, 0},
		{"bootstrap fails", traefikConfigOps{getenv: env("/d"), bootstrap: func(context.Context, string) error { return errors.New("x") }}, nil, 1},
		{"no data dir", traefikConfigOps{getenv: env(""), bootstrap: func(context.Context, string) error { t.Fatal("bootstrap ran"); return nil }}, nil, 1},
		{"extra args", traefikConfigOps{getenv: env("/d"), bootstrap: func(context.Context, string) error { return nil }}, []string{"x"}, 2},
	}
	for _, c := range cases {
		var stderr bytes.Buffer
		if got := runTraefikConfigWith(c.args, c.ops, &stderr); got != c.want {
			t.Errorf("%s: exit %d, want %d (stderr %q)", c.name, got, c.want, stderr.String())
		}
	}
}

func TestTraefikConfigSubcommandListed(t *testing.T) {
	if findSubcommand("traefik-config") == nil {
		t.Fatal("traefik-config is not a subcommand")
	}
}
