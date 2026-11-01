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
