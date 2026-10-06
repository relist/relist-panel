package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"relist-panel/internal/auth"
	"relist-panel/internal/config"
	"relist-panel/internal/database"
)

const resetTwoFactorUsage = "usage: relist-panel reset-2fa [--email ADDRESS]"

// runResetTwoFactor implements `relist-panel reset-2fa`. It returns the exit
// code: 0 on success, 1 for a failed reset or setup, 2 for a usage error.
func runResetTwoFactor(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reset-2fa", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, resetTwoFactorUsage)
	}
	email := flags.String("email", "", "email of the admin account to reset")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(stderr, resetTwoFactorUsage)
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "reset-2fa: %v\n", err)
		return 1
	}

	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		fmt.Fprintf(stderr, "reset-2fa: %v\n", err)
		return 1
	}
	defer db.Close()

	result, err := auth.ResetTwoFactor(context.Background(), db, *email)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountNotFound):
			fmt.Fprintf(stderr, "reset-2fa: no admin account matches %q\n", *email)
		case errors.Is(err, auth.ErrEmailRequired):
			fmt.Fprintf(stderr, "reset-2fa: more than one admin account exists; pass --email\n")
		default:
			fmt.Fprintf(stderr, "reset-2fa: %v\n", err)
		}
		return 1
	}

	state := "two-factor was off"
	if result.WasEnabled {
		state = "two-factor was on"
	}
	fmt.Fprintf(stdout, "Reset two-factor for %s (%s); revoked %d sessions.\n",
		result.Email, state, result.SessionsRevoked)
	return 0
}
