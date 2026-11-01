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
		{[]string{"create-admin", "--generate"}, actionSubcommand},
		{[]string{"migration", "status"}, actionSubcommand},
		{[]string{"maintenance-page"}, actionSubcommand},
		{[]string{"panel-update", "apply", "--run", "x"}, actionSubcommand},
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

func TestParseApplyArgs(t *testing.T) {
	cases := []struct {
		args []string
		run  string
		ok   bool
	}{
		{[]string{"apply", "--run", "20261101T120000Z"}, "20261101T120000Z", true},
		{[]string{"apply", "--run=20261101T120000Z"}, "20261101T120000Z", true},
		{nil, "", false},
		{[]string{"apply"}, "", false},
		{[]string{"apply", "--run", "../../etc"}, "", false},
		{[]string{"apply", "--run", "20261101T120000Z", "extra"}, "", false},
		{[]string{"rollback", "--run", "20261101T120000Z"}, "", false},
	}
	for _, c := range cases {
		if run, ok := parseApplyArgs(c.args); run != c.run && ok || ok != c.ok {
			t.Errorf("parseApplyArgs(%q) = %q, %v", c.args, run, ok)
		}
	}
}
