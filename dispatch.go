package main

import (
	"fmt"
	"io"
)

// action is what main() does for a given argument list.
type action int

const (
	actionServer action = iota
	actionHelp
	actionVersion
	actionUsageError
	actionSubcommand
)

// subcommands are the first arguments main() hands to their own runners, each
// with the one-line description usage prints.
var subcommands = []struct{ name, summary string }{
	{"reset-2fa", "turn off two-factor sign-in for the admin"},
	{"reset-password", "set a new admin password and sign every session out"},
	{"create-admin", "create the first admin account"},
	{"migration", "drive a server Migration through the running panel"},
	{"maintenance-page", "answer 503 with the maintenance page (used by a Migration)"},
}

// serverFlags are the only flags the server itself reads (internal/config).
var serverFlags = []string{"--insecure-migration-source"}

// decide maps the arguments after the program name to an action, without
// loading config or touching the data directory. Anything that is neither a
// known subcommand nor made only of server flags must never start the server.
func decide(args []string) action {
	if len(args) == 0 {
		return actionServer
	}
	switch args[0] {
	case "-h", "--help", "help":
		return actionHelp
	case "--version", "version":
		return actionVersion
	}
	for _, s := range subcommands {
		if args[0] == s.name {
			return actionSubcommand
		}
	}
	for _, a := range args {
		known := false
		for _, f := range serverFlags {
			known = known || a == f
		}
		if !known {
			return actionUsageError
		}
	}
	return actionServer
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: relist-panel [--insecure-migration-source]")
	fmt.Fprintln(w, "       relist-panel <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "With no command the panel server starts.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Server flags:")
	fmt.Fprintln(w, "  --insecure-migration-source  allow a server Migration from an old host over plain HTTP")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, s := range subcommands {
		fmt.Fprintf(w, "  %-17s%s\n", s.name, s.summary)
	}
	fmt.Fprintln(w, "  help, -h, --help  print this help")
	fmt.Fprintln(w, "  version           print the version")
}
