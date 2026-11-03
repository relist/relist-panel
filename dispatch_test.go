package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		args []string
		want action
	}{
		{nil, actionServer},
		{[]string{"--help"}, actionHelp},
		{[]string{"-h"}, actionHelp},
		{[]string{"help"}, actionHelp},
		{[]string{"--version"}, actionVersion},
		{[]string{"version"}, actionVersion},
		{[]string{"reset-2fa"}, actionSubcommand},
		{[]string{"reset-password"}, actionSubcommand},
		{[]string{"change-email", "--new-email", "a@example.com"}, actionSubcommand},
		{[]string{"create-admin", "--generate"}, actionSubcommand},
		{[]string{"migration", "status"}, actionSubcommand},
		{[]string{"maintenance-page"}, actionSubcommand},
		{[]string{"setup", "--dir", "/x"}, actionSubcommand},
		{[]string{"update", "--preflight", "--dir", "/x"}, actionSubcommand},
		{[]string{"panel-update", "apply", "--run", "x"}, actionUsageError},
		{[]string{"--insecure-migration-source"}, actionServer},
		{[]string{"--bogus"}, actionUsageError},
		{[]string{"--insecure-migration-source", "--bogus"}, actionUsageError},
		{[]string{"serve"}, actionUsageError},
	}
	for _, c := range cases {
		if got := decide(c.args); got != c.want {
			t.Errorf("decide(%q) = %d, want %d", c.args, got, c.want)
		}
	}
}

func TestUsageListsEverything(t *testing.T) {
	var b bytes.Buffer
	printUsage(&b)
	for _, s := range subcommands {
		if !strings.Contains(b.String(), s.name) {
			t.Errorf("usage lacks %s", s.name)
		}
	}
	if !strings.Contains(b.String(), "--insecure-migration-source") {
		t.Error("usage lacks the server flag")
	}
}

// Every listed subcommand has a runner, so deciding actionSubcommand can never
// fall through to starting the server.
func TestEverySubcommandHasARunner(t *testing.T) {
	for _, s := range subcommands {
		if s.run == nil || findSubcommand(s.name) == nil {
			t.Errorf("%s has no runner", s.name)
		}
	}
	if findSubcommand("serve") != nil {
		t.Error("serve is not a subcommand")
	}
}

func TestDispatchListsUpdateNotPanelUpdate(t *testing.T) {
	if findSubcommand("update") == nil {
		t.Error("update is not a subcommand")
	}
	if findSubcommand("panel-update") != nil {
		t.Error("panel-update must be gone")
	}
	var b bytes.Buffer
	printUsage(&b)
	if !strings.Contains(b.String(), "update") || strings.Contains(b.String(), "panel-update") {
		t.Errorf("usage = %s", b.String())
	}
}
