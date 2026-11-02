package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/panelupdate"
)

const updateUsage = `usage: relist-panel update --preflight [--dir PATH] [--project NAME] [--file PATH]... [--json]
       relist-panel update --report ID --run RUN [--steps-done ID,...] [--dir PATH] [--project NAME] [--file PATH]...

--preflight checks what a Panel update would do and writes a report; it changes
nothing else. Exit 0: ready. Exit 1: not ready. Exit 2: usage error. Exit 3: up
to date.

--report applies the report: it re-checks, asks the panel to go quiet, backs up,
writes the files, runs docker compose up, waits for health and rolls back on
failure. Exit 0: succeeded. Exit 1: any other end.

Run it in a Container of the panel image, with the Docker socket, the Install
directory and the data directory (RELIST_DATA_DIR) mounted at their host paths.`

// updateFlags is what the command line says.
type updateFlags struct {
	preflight bool
	report    string
	run       string
	steps     []string
	asJSON    bool
	install   panelupdate.InstallArgs
}

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// parseUpdateFlags parses args. code is the exit code to return when ok is
// false: 0 for --help, 2 for a usage error.
func parseUpdateFlags(args []string, stdout, stderr io.Writer) (f updateFlags, code int, ok bool) {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	var files stringList
	var steps string
	fs.BoolVar(&f.preflight, "preflight", false, "")
	fs.StringVar(&f.report, "report", "", "")
	fs.StringVar(&f.run, "run", "", "")
	fs.StringVar(&steps, "steps-done", "", "")
	fs.StringVar(&f.install.Dir, "dir", "", "")
	fs.StringVar(&f.install.Project, "project", "", "")
	fs.Var(&files, "file", "")
	fs.BoolVar(&f.asJSON, "json", false, "")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprintln(stdout, updateUsage)
			return f, 0, false
		}
		fmt.Fprintln(stderr, updateUsage)
		return f, 2, false
	}
	usage := func(msg string) (updateFlags, int, bool) {
		fmt.Fprintf(stderr, "update: %s\n%s\n", msg, updateUsage)
		return f, 2, false
	}
	if fs.NArg() > 0 {
		return usage("unexpected argument " + fs.Arg(0))
	}
	switch {
	case f.preflight == (f.report != ""):
		return usage("give exactly one of --preflight and --report")
	case f.report != "" && !panelupdate.ValidReportID(f.report):
		return usage("--report needs a report id as --preflight prints it")
	case f.report != "" && !panelupdate.ValidRunID(f.run):
		return usage("--report needs --run with a run id like 20261101T120000Z")
	case f.preflight && f.run != "":
		return usage("--run goes with --report")
	}
	f.install.Files = files
	if f.install.Dir == "" && len(files) > 0 {
		f.install.Dir = filepath.Dir(files[0])
	}
	if f.install.Dir == "" {
		return usage("--dir is required")
	}
	if !filepath.IsAbs(f.install.Dir) {
		return usage("--dir must be an absolute path")
	}
	for _, s := range strings.Split(steps, ",") {
		if s = strings.TrimSpace(s); s != "" {
			f.steps = append(f.steps, s)
		}
	}
	return f, 0, true
}

// updateOps is what the update command does with Docker; tests replace it so
// the modes never start a server or reach Docker.
type updateOps struct {
	getenv    func(string) string
	preflight func(ctx context.Context, dataDir string, a panelupdate.InstallArgs) (*panelupdate.Report, error)
	apply     func(ctx context.Context, dataDir string, f updateFlags) (panelupdate.RunStatus, error)
}

// runUpdate is `relist-panel update`.
func runUpdate(args []string) int {
	return runUpdateWith(args, realUpdateOps(), os.Stdout, os.Stderr)
}

func runUpdateWith(args []string, ops updateOps, stdout, stderr io.Writer) int {
	f, code, ok := parseUpdateFlags(args, stdout, stderr)
	if !ok {
		return code
	}
	dataDir := ops.getenv("RELIST_DATA_DIR")
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		fmt.Fprintln(stderr, "update: RELIST_DATA_DIR must be set to the panel's data directory, an absolute path")
		return 2
	}
	ctx := context.Background()
	if f.preflight {
		r, err := ops.preflight(ctx, dataDir, f.install)
		if err != nil {
			fmt.Fprintf(stderr, "update: %v\n", err)
			return 2
		}
		if f.asJSON {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(r); err != nil {
				fmt.Fprintf(stderr, "update: %v\n", err)
				return 2
			}
		} else {
			printReport(stdout, r)
		}
		return r.ExitCode()
	}
	st, err := ops.apply(ctx, dataDir, f)
	if err != nil {
		fmt.Fprintf(stderr, "update: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Update %s: %s\n", st.State, summarizeRun(st))
	if st.State != panelupdate.StateSucceeded {
		return 1
	}
	return 0
}

func summarizeRun(st panelupdate.RunStatus) string {
	switch st.State {
	case panelupdate.StateSucceeded:
		out := fmt.Sprintf("the panel runs %s", st.ToVersion)
		if st.Note != "" {
			out += " (" + st.Note + ")"
		}
		return out
	case panelupdate.StateRolledBack:
		return "rolled back to " + st.FromVersion + ": " + st.Error
	case panelupdate.StateRollbackFailed:
		return "the update failed (" + st.Error + ") and so did the rollback (" + st.RollbackError + ")"
	}
	return st.Error
}

// printReport writes the text form of a preflight report. install.sh reads the
// "Report:" and "Manual step" lines.
func printReport(w io.Writer, r *panelupdate.Report) {
	fmt.Fprintf(w, "Running version: %s\n", r.FromVersion)
	if r.ToVersion != "" {
		fmt.Fprintf(w, "Latest version:  %s\n", r.ToVersion)
	}
	if r.NotesURL != "" {
		fmt.Fprintf(w, "Release notes:   %s\n", r.NotesURL)
	}
	switch {
	case r.UpToDate:
		fmt.Fprintln(w, "The panel is up to date.")
		return
	case r.Failed != nil && r.Failed.Step == panelupdate.StepManifest:
		fmt.Fprintf(w, "Not ready (%s): %s\n", r.Failed.Step, r.Failed.Message)
		return
	case r.NeedsManualUpdate:
		fmt.Fprintf(w, "This version needs a manual update (unknown requirement: %s). See %s\n", strings.Join(r.UnknownKinds, ", "), r.ReadmeURL)
		return
	}
	switch r.Change {
	case panelupdate.ChangeNotEdited:
		fmt.Fprintln(w, "Install file:    not edited; the new file replaces it")
	case panelupdate.ChangeEditedMerged:
		fmt.Fprintln(w, "Install file:    edited, merged with the new version")
	case panelupdate.ChangeEditedConflict:
		fmt.Fprintln(w, "Install file:    edited, CONFLICT with the new version")
	}
	if r.Diff != "" {
		fmt.Fprintln(w, "Change to the Install file:")
		fmt.Fprintln(w, strings.TrimRight(r.Diff, "\n"))
	}
	if r.Conflict != nil {
		fmt.Fprintf(w, "The merge has %d conflicting hunk(s):\n%s\n", r.Conflict.Hunks, strings.TrimRight(r.Conflict.Merged, "\n"))
	}
	if len(r.Invariants) > 0 {
		fmt.Fprintln(w, "Install requirements:")
		for _, inv := range r.Invariants {
			if inv.OK {
				fmt.Fprintf(w, "  ok    %s\n", inv.Text)
				continue
			}
			line := fmt.Sprintf("  FAIL  %s", inv.Text)
			if inv.Detail != "" {
				line += ": " + inv.Detail
			}
			if inv.Source != "" {
				line += " (set by " + inv.Source + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(r.Recreated) > 0 {
		fmt.Fprintf(w, "Services recreated: %s\n", strings.Join(r.Recreated, ", "))
	}
	if r.TraefikRecreated {
		fmt.Fprintln(w, "Warning: Traefik is recreated, so every Domain drops for a few seconds.")
	}
	for _, s := range r.ManualSteps {
		fmt.Fprintf(w, "Manual step [%s]: %s\n", s.ID, s.Text)
	}
	if r.Failed != nil {
		fmt.Fprintf(w, "Not ready (%s): %s\n", r.Failed.Step, r.Failed.Message)
	} else if r.Ready {
		fmt.Fprintln(w, "Ready to update.")
	} else {
		fmt.Fprintln(w, "Not ready: the Install does not meet this version's requirements.")
	}
	fmt.Fprintf(w, "Report: %s\n", r.ID)
}

func realUpdateOps() updateOps {
	host := dockerexec.NewPanelUpdaterHost(dockerexec.DefaultSocketPath)
	checker := func(dataDir string) *panelupdate.Checker {
		return &panelupdate.Checker{
			DataDir: dataDir, Version: version, InstallFile: installFile,
			ManifestURL: panelupdate.ManifestURLFromEnv(),
			Docker:      dockerexec.NewPanelDocker(dockerexec.DefaultSocketPath),
			Compose:     dockerexec.LocalCompose{},
		}
	}
	return updateOps{
		getenv: os.Getenv,
		preflight: func(ctx context.Context, dataDir string, a panelupdate.InstallArgs) (*panelupdate.Report, error) {
			in, err := panelupdate.ResolveInstall(ctx, a, host)
			if err != nil {
				return nil, err
			}
			r, _, err := checker(dataDir).Check(ctx, in)
			if err != nil {
				return nil, err
			}
			if err := panelupdate.WriteReport(dataDir, r); err != nil {
				return nil, fmt.Errorf("write the report: %w", err)
			}
			return r, nil
		},
		apply: func(ctx context.Context, dataDir string, f updateFlags) (panelupdate.RunStatus, error) {
			in, err := panelupdate.ResolveInstall(ctx, f.install, host)
			if err != nil {
				return panelupdate.RunStatus{}, err
			}
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
			defer signal.Stop(sig)
			u := &panelupdate.Updater{
				DataDir: dataDir, Host: host, Checker: checker(dataDir), Domain: installDomain(in),
				Signals: sig, Logf: log.Printf,
			}
			return u.Apply(ctx, panelupdate.ApplyArgs{Run: f.run, Report: f.report, StepsDone: f.steps, In: in})
		},
	}
}

// installDomain is RELIST_DOMAIN from the Install's .env, the Host and SNI of
// the health request; panel.localhost when it is not set, as the Install file
// defaults it.
func installDomain(in panelupdate.Install) string {
	files, err := panelupdate.ReadLocalFiles(in)
	if err == nil {
		if d := panelupdate.EnvValue(files.Env, "RELIST_DOMAIN"); d != "" {
			return d
		}
	}
	return "panel.localhost"
}
