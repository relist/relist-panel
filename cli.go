package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"relist-panel/internal/audit"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

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
			fmt.Fprintf(stderr, "reset-2fa: no user matches %q\n", *email)
		case errors.Is(err, auth.ErrEmailRequired):
			fmt.Fprintf(stderr, "reset-2fa: more than one user exists; pass --email\n")
		default:
			fmt.Fprintf(stderr, "reset-2fa: %v\n", err)
		}
		return 1
	}

	recordCLI(db, "cli.reset_2fa", map[string]any{"email": result.Email})
	state := "two-factor was off"
	if result.WasEnabled {
		state = "two-factor was on"
	}
	fmt.Fprintf(stdout, "Reset two-factor for %s (%s); revoked %d sessions.\n",
		result.Email, state, result.SessionsRevoked)
	return 0
}

const changeEmailUsage = "usage: relist-panel change-email --new-email ADDRESS [--email ADDRESS]"

// runChangeEmail implements `relist-panel change-email`. It returns the exit
// code: 0 on success, 1 for a failed change, 2 for a usage error.
func runChangeEmail(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("change-email", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, changeEmailUsage)
	}
	email := flags.String("email", "", "current email of the admin account")
	newEmail := flags.String("new-email", "", "the new email address")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 || *newEmail == "" {
		fmt.Fprintln(stderr, changeEmailUsage)
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "change-email: %v\n", err)
		return 1
	}
	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		fmt.Fprintf(stderr, "change-email: %v\n", err)
		return 1
	}
	defer db.Close()

	result, err := auth.ChangeEmail(context.Background(), db, *email, *newEmail)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountNotFound):
			fmt.Fprintf(stderr, "change-email: no user matches %q\n", *email)
		case errors.Is(err, auth.ErrEmailRequired):
			fmt.Fprintln(stderr, "change-email: more than one user exists; pass --email")
		default:
			fmt.Fprintf(stderr, "change-email: %v\n", err)
		}
		return 1
	}
	if result.OldEmail == result.NewEmail {
		fmt.Fprintf(stdout, "Email is already %s; nothing changed.\n", result.NewEmail)
		return 0
	}
	recordCLI(db, "cli.change_email", map[string]any{"email": result.NewEmail, "oldEmail": result.OldEmail})
	fmt.Fprintf(stdout, "Changed email from %s to %s.\n", result.OldEmail, result.NewEmail)
	return 0
}

const createAdminUsage = "usage: relist-panel create-admin [--email ADDRESS] [--generate | --password-stdin]"

// console is what create-admin needs of the terminal on stdin: whether there
// is one, and a read that does not echo.
type console struct {
	isTerminal   bool
	readPassword func() (string, error)
}

// stdinConsole is the real terminal on os.Stdin.
func stdinConsole() console {
	fd := os.Stdin.Fd()
	return console{
		isTerminal: term.IsTerminal(fd),
		readPassword: func() (string, error) {
			password, err := term.ReadPassword(fd)
			return string(password), err
		},
	}
}

// runCreateAdmin implements `relist-panel create-admin`, the only way to make
// the first admin. Prompts and hints go to stderr, so stdout holds only the
// result and, when generated, the password. It returns the exit code: 0 on
// success, 1 for a refused or failed create, 2 for a usage error.
func runCreateAdmin(args []string, stdin io.Reader, stdout, stderr io.Writer, tty console) int {
	flags := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, createAdminUsage)
	}
	email := flags.String("email", "", "email of the admin account")
	generate := flags.Bool("generate", false, "generate the password and print it once")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from the first line of stdin")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 || (*generate && *passwordStdin) || (*passwordStdin && *email == "") {
		fmt.Fprintln(stderr, createAdminUsage)
		return 2
	}
	if !tty.isTerminal && !*passwordStdin && (!*generate || *email == "") {
		fmt.Fprintln(stderr, "create-admin: stdin is not a terminal; pass --password-stdin or --generate")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "create-admin: %v\n", err)
		return 1
	}
	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		fmt.Fprintf(stderr, "create-admin: %v\n", err)
		return 1
	}
	defer db.Close()

	ctx := context.Background()
	// Say so before asking for anything that would be thrown away.
	if exists, err := auth.AdminExists(ctx, db); err != nil {
		fmt.Fprintf(stderr, "create-admin: %v\n", err)
		return 1
	} else if exists {
		fmt.Fprintln(stderr, "create-admin: an admin account already exists")
		return 1
	}

	lines := bufio.NewReader(stdin)
	if *email == "" {
		fmt.Fprint(stderr, "Email: ")
		if *email, err = readLine(lines); err != nil {
			fmt.Fprintf(stderr, "create-admin: read the email: %v\n", err)
			return 1
		}
	}

	var password string
	switch {
	case *passwordStdin:
		if password, err = readLine(lines); err != nil {
			fmt.Fprintf(stderr, "create-admin: read the password: %v\n", err)
			return 1
		}
	case !*generate:
		fmt.Fprint(stderr, "Password (leave empty to generate one): ")
		password, err = tty.readPassword()
		fmt.Fprintln(stderr)
		if err != nil {
			fmt.Fprintf(stderr, "create-admin: read the password: %v\n", err)
			return 1
		}
		if password != "" {
			fmt.Fprint(stderr, "Confirm password: ")
			confirm, err := tty.readPassword()
			fmt.Fprintln(stderr)
			if err != nil {
				fmt.Fprintf(stderr, "create-admin: read the password: %v\n", err)
				return 1
			}
			if confirm != password {
				fmt.Fprintln(stderr, "create-admin: passwords do not match")
				return 1
			}
		}
	}
	generated := password == ""
	if generated {
		if password, err = auth.GeneratePassword(); err != nil {
			fmt.Fprintf(stderr, "create-admin: %v\n", err)
			return 1
		}
	}

	created, err := auth.CreateAdmin(ctx, db, *email, password, time.Now)
	if err != nil {
		if errors.Is(err, auth.ErrAdminAlreadyExists) {
			fmt.Fprintln(stderr, "create-admin: an admin account already exists")
		} else {
			fmt.Fprintf(stderr, "create-admin: %v\n", err)
		}
		return 1
	}
	recordCLI(db, "cli.create_admin", map[string]any{"email": created})
	fmt.Fprintf(stdout, "Created admin %s.\n", created)
	if generated {
		fmt.Fprintf(stdout, "Password: %s\n", password)
		fmt.Fprintln(stderr, "Sign in, then change it in Account.")
	}
	return 0
}

const resetPasswordUsage = "usage: relist-panel reset-password [--email ADDRESS] [--generate | --password-stdin]"

// runResetPassword implements `relist-panel reset-password`, the recovery path
// for a lost password. Prompts and hints go to stderr, so stdout holds only the
// result and, when generated, the password. It returns the exit code: 0 on
// success, 1 for a failed reset, 2 for a usage error.
func runResetPassword(args []string, stdin io.Reader, stdout, stderr io.Writer, tty console) int {
	flags := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, resetPasswordUsage)
	}
	email := flags.String("email", "", "email of the admin account to reset")
	generate := flags.Bool("generate", false, "generate the password and print it once")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from the first line of stdin")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 || (*generate && *passwordStdin) {
		fmt.Fprintln(stderr, resetPasswordUsage)
		return 2
	}
	if !tty.isTerminal && !*passwordStdin && !*generate {
		fmt.Fprintln(stderr, "reset-password: stdin is not a terminal; pass --password-stdin or --generate")
		return 2
	}

	var password string
	var err error
	switch {
	case *passwordStdin:
		if password, err = readLine(bufio.NewReader(stdin)); err != nil {
			fmt.Fprintf(stderr, "reset-password: read the password: %v\n", err)
			return 1
		}
	case !*generate:
		fmt.Fprint(stderr, "New password (leave empty to generate one): ")
		password, err = tty.readPassword()
		fmt.Fprintln(stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reset-password: read the password: %v\n", err)
			return 1
		}
		if password != "" {
			fmt.Fprint(stderr, "Confirm password: ")
			confirm, err := tty.readPassword()
			fmt.Fprintln(stderr)
			if err != nil {
				fmt.Fprintf(stderr, "reset-password: read the password: %v\n", err)
				return 1
			}
			if confirm != password {
				fmt.Fprintln(stderr, "reset-password: passwords do not match")
				return 1
			}
		}
	}
	generated := password == ""
	if generated {
		if password, err = auth.GeneratePassword(); err != nil {
			fmt.Fprintf(stderr, "reset-password: %v\n", err)
			return 1
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "reset-password: %v\n", err)
		return 1
	}
	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		fmt.Fprintf(stderr, "reset-password: %v\n", err)
		return 1
	}
	defer db.Close()

	result, err := auth.ResetPassword(context.Background(), db, *email, password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountNotFound):
			fmt.Fprintf(stderr, "reset-password: no user matches %q\n", *email)
		case errors.Is(err, auth.ErrEmailRequired):
			fmt.Fprintln(stderr, "reset-password: more than one user exists; pass --email")
		default:
			fmt.Fprintf(stderr, "reset-password: %v\n", err)
		}
		return 1
	}
	recordCLI(db, "cli.reset_password", map[string]any{"email": result.Email})
	fmt.Fprintf(stdout, "Reset password for %s; revoked %d sessions.\n", result.Email, result.SessionsRevoked)
	if generated {
		fmt.Fprintf(stdout, "Password: %s\n", password)
		fmt.Fprintln(stderr, "Sign in, then change it in Account.")
	}
	return 0
}

// readLine reads one line, without its line ending; a last line with no
// newline counts.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// recordCLI writes a host command's Audit event, actor cli. A failure is
// logged and never fails the command: the change itself is already done.
func recordCLI(db *sql.DB, action string, detail map[string]any) {
	if err := audit.Record(context.Background(), db, audit.Event{Actor: audit.ActorCLI, Action: action, Outcome: audit.OutcomeOK, Detail: detail}); err != nil {
		log.Printf("audit: %s: %v", action, err)
	}
}
