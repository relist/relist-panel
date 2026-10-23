// Package migrationcli is `relist-panel migration` (ADR-0019): the commands
// that drive a server Migration from a terminal in the panel container,
// through the control socket.
package migrationcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"relist-panel/internal/config"
	"relist-panel/internal/migration"
	"relist-panel/internal/migrationctl"
	"relist-panel/internal/migrationtui"
)

const usage = `usage: relist-panel migration <command>

  serve             on the old server: create a token, then follow the migration
  pull              on the new server: connect, preview, start and follow it
  status [--watch]  where the migration is on this server
  resume            on the old server: cancel the migration, or resume after it

Run them in the panel container, with a terminal:
  docker compose exec panel relist-panel migration <command>`

const needsTerminal = "needs a terminal: run it with docker compose exec, or docker exec -it"

// Env is what the commands use from the outside world.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	IsTerminal     func() bool
	Socket         string // the control socket
	Domain         string // RELIST_DOMAIN, for this panel's URL in pull
	// Run runs a TUI to its end and returns the final model.
	Run func(m tea.Model, in io.Reader, out io.Writer) (tea.Model, error)
	// WatchEvery is how often status --watch reprints; 0 means 2 s.
	WatchEvery time.Duration
}

// DefaultEnv is the process's own terminal, and the socket and settings cfg
// holds.
func DefaultEnv(cfg config.Config) Env {
	return Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		IsTerminal: func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) },
		Socket:     cfg.ControlSocket,
		Domain:     cfg.Install.Domain,
		Run: func(m tea.Model, in io.Reader, out io.Writer) (tea.Model, error) {
			return tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out)).Run()
		},
	}
}

// Run runs `relist-panel migration args...` and returns the exit status.
func Run(args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve", "pull", "resume":
		if len(args) != 1 {
			fmt.Fprintln(env.Stderr, usage)
			return 2
		}
		if !env.IsTerminal() {
			fmt.Fprintln(env.Stderr, needsTerminal)
			return 2
		}
		if !panelThere(env) {
			return 1
		}
		return runTUI(args[0], env)
	case "status":
		watch := len(args) == 2 && args[1] == "--watch"
		if len(args) > 2 || (len(args) == 2 && !watch) {
			fmt.Fprintln(env.Stderr, usage)
			return 2
		}
		if !panelThere(env) {
			return 1
		}
		return status(env, watch)
	case "api":
		// Hidden: one raw request over the socket, for probes and debugging.
		if len(args) < 3 || len(args) > 4 {
			fmt.Fprintln(env.Stderr, "usage: relist-panel migration api METHOD PATH [JSON]")
			return 2
		}
		if !panelThere(env) {
			return 1
		}
		var body []byte
		if len(args) == 4 {
			body = []byte(args[3])
		}
		code, out, err := migrationctl.New(env.Socket).Raw(context.Background(), strings.ToUpper(args[1]), args[2], body)
		if err != nil {
			fmt.Fprintln(env.Stderr, err)
			return 1
		}
		fmt.Fprintln(env.Stdout, strings.TrimRight(string(out), "\n"))
		if code >= 400 {
			return 1
		}
		return 0
	}
	fmt.Fprintln(env.Stderr, usage)
	return 2
}

// panelThere reports, and says when not, whether the control socket exists.
func panelThere(env Env) bool {
	if _, err := os.Stat(env.Socket); err != nil {
		fmt.Fprintf(env.Stderr, "the panel is not running in this container (no %s)\n", env.Socket)
		return false
	}
	return true
}

func runTUI(name string, env Env) int {
	c := migrationctl.New(env.Socket)
	var m tea.Model
	switch name {
	case "serve":
		m = migrationtui.NewServe(c)
	case "pull":
		self := ""
		if env.Domain != "" {
			self = "https://" + env.Domain
		}
		m = migrationtui.NewPull(c, self)
	default:
		m = migrationtui.NewResume(c)
	}
	final, err := env.Run(m, env.Stdin, env.Stdout)
	if err != nil {
		fmt.Fprintf(env.Stderr, "migration %s: %v\n", name, err)
		return 1
	}
	if note := migrationtui.QuitNote(final); note != "" {
		fmt.Fprintln(env.Stdout, note)
	}
	return 0
}

// status prints where the Migration is; with watch, again every WatchEvery
// until it is done, paused or there is none. A panel re-executing at
// finalize is waited for, up to two minutes.
func status(env Env, watch bool) int {
	c := migrationctl.New(env.Socket)
	every := env.WatchEvery
	if every == 0 {
		every = 2 * time.Second
	}
	var goneSince time.Time
	last := ""
	for {
		text, stop, err := describe(c)
		switch {
		case err != nil && watch && errors.Is(err, migrationctl.ErrNoPanel):
			if goneSince.IsZero() {
				goneSince = time.Now()
				fmt.Fprintln(env.Stdout, "Restarting the panel…")
			} else if time.Since(goneSince) > 2*time.Minute {
				fmt.Fprintln(env.Stderr, "the panel did not come back: run docker compose up -d in the install directory")
				return 1
			}
		case err != nil:
			fmt.Fprintln(env.Stderr, err)
			return 1
		default:
			goneSince = time.Time{}
			if text != last {
				fmt.Fprintln(env.Stdout, text)
				last = text
			}
			if !watch || stop != "" {
				if stop == "paused" {
					return 1
				}
				return 0
			}
		}
		time.Sleep(every)
	}
}

// describe reads both sides and formats them; stop is "" while there is
// something to wait for, else "none", "done" or "paused".
func describe(c *migrationctl.Client) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := c.Progress(ctx)
	if err != nil {
		return "", "", err
	}
	s, err := c.Status(ctx)
	if err != nil {
		return "", "", err
	}
	a, _ := c.Activity(ctx)
	stop := ""
	switch {
	case p.Phase == migration.PhaseDone, s.Step == migration.StepMigrated && p.Phase == migration.PhaseIdle:
		stop = "done"
	case p.Phase == migration.PhasePaused:
		stop = "paused"
	case p.Phase == migration.PhaseIdle && s.Step == migration.StepNone && s.TokenExpiresAt == nil:
		stop = "none"
	}
	return FormatStatus(p, s, a), stop, nil
}
