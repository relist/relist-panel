package main

import (
	"fmt"
	"io"
	"os"
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

// subcommand is a first argument main() hands to its own runner, with the
// one-line description usage prints. run returns the exit code. One table
// serves both dispatch and usage, so a subcommand cannot be listed without a
// runner, and an unlisted one never reaches the server.
type subcommand struct {
	name, summary string
	run           func(args []string) int
}

var subcommands = []subcommand{
	{"reset-2fa", "turn off two-factor sign-in for a user",
		func(a []string) int { return runResetTwoFactor(a, os.Stdout, os.Stderr) }},
	{"reset-password", "set a new password for a user and sign their sessions out",
		func(a []string) int { return runResetPassword(a, os.Stdin, os.Stdout, os.Stderr, stdinConsole()) }},
	{"change-email", "change a user's sign-in email",
		func(a []string) int { return runChangeEmail(a, os.Stdout, os.Stderr) }},
	{"create-admin", "create the first user, the owner",
		func(a []string) int { return runCreateAdmin(a, os.Stdin, os.Stdout, os.Stderr, stdinConsole()) }},
	{"migration", "drive a server Migration through the running panel", runMigration},
	{"maintenance-page", "answer 503 with the maintenance page (used by a Migration)", runMaintenancePage},
	{"setup", "write the Install file and .env for a new install",
		func(a []string) int { return runSetup(a, os.Stdout, os.Stderr) }},
	{"update", "check or apply a Panel update (runs in its own Container)", runUpdate},
	{"traefik-config", "write Traefik's static config before Traefik starts (runs in its own Container)", runTraefikConfig},
}

// findSubcommand is the subcommand named name, nil when there is none.
func findSubcommand(name string) *subcommand {
	for i := range subcommands {
		if subcommands[i].name == name {
			return &subcommands[i]
		}
	}
	return nil
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
	if findSubcommand(args[0]) != nil {
		return actionSubcommand
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
