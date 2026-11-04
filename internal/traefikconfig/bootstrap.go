package traefikconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ArgsReader reads the running Traefik's CLI args.
type ArgsReader interface {
	Args(ctx context.Context) ([]string, error)
}

// Bootstrap is `relist-panel traefik-config`, run before Traefik on every
// `docker compose up`. It creates the Traefik override if there is none (from
// the running Traefik's flags on an update, from .env on a fresh install) and
// regenerates traefik.yml. A Traefik override that cannot be used keeps the
// last traefik.yml and is reported in apply.json, so `up` does not fail over
// it. The error is only for "no usable traefik.yml".
func Bootstrap(ctx context.Context, p Paths, t ArgsReader, noTraefik func(error) bool, getenv func(string) string, logw io.Writer) error {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	var warnings []string
	override, err := os.ReadFile(p.Override())
	if errors.Is(err, os.ErrNotExist) {
		override, warnings = firstOverride(ctx, t, noTraefik, getenv)
		if err := WriteAtomic(p.Override(), override); err != nil {
			return err
		}
		fmt.Fprintf(logw, "traefik-config: created %s\n", p.Override())
	} else if err != nil {
		return keepOrFail(p, logw, fmt.Sprintf("could not read the Traefik override: %v", err))
	}

	effective, problems, err := Generate(override)
	if err != nil {
		problems = []Problem{ParseErrorProblem(err)}
	}
	if len(problems) > 0 {
		return keepOrFail(p, logw, "the Traefik override cannot be used, so traefik.yml was not changed: "+describe(problems))
	}
	if err := WriteAtomic(p.Effective(), effective); err != nil {
		return err
	}
	fmt.Fprintf(logw, "traefik-config: wrote %s\n", p.Effective())
	return RecordBootstrapWarning(p, strings.Join(warnings, "; "))
}

// firstOverride converts the running Traefik's flags, or seeds from .env when
// there is no Traefik, it already reads the file, or its args cannot be read.
func firstOverride(ctx context.Context, t ArgsReader, noTraefik func(error) bool, getenv func(string) string) ([]byte, []string) {
	seed := func() []byte { return Seed(getenv("RELIST_ACME_EMAIL"), getenv("TRAEFIK_TRUSTED_IPS")) }
	if t == nil {
		return seed(), nil
	}
	args, err := t.Args(ctx)
	switch {
	case err != nil && noTraefik(err):
		return seed(), nil
	case err != nil:
		return seed(), []string{fmt.Sprintf("could not read the running Traefik's flags (%v); the Traefik override was started from .env", err)}
	}
	for _, a := range args {
		if strings.HasPrefix(strings.ToLower(a), "--configfile=") {
			return seed(), nil
		}
	}
	override, problems := FromArgs(args)
	var warnings []string
	for _, pr := range problems {
		warnings = append(warnings, pr.Key+": "+pr.Message)
	}
	return override, warnings
}

// keepOrFail keeps an existing traefik.yml and records why, or fails when
// there is none: Traefik would not start without it.
func keepOrFail(p Paths, logw io.Writer, why string) error {
	fmt.Fprintln(logw, "traefik-config: "+why)
	if _, err := os.Stat(p.Effective()); err != nil {
		return fmt.Errorf("%s, and there is no %s to keep", why, p.Effective())
	}
	return RecordBootstrapWarning(p, why)
}

func describe(problems []Problem) string {
	parts := make([]string, 0, len(problems))
	for _, pr := range problems {
		s := pr.Message
		if pr.Line > 0 && !strings.Contains(s, "line ") {
			s = fmt.Sprintf("line %d: %s", pr.Line, s)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// RecordBootstrapWarning sets (or, with "", clears) apply.json's
// bootstrapWarning, keeping the rest of the file.
func RecordBootstrapWarning(p Paths, msg string) error {
	m := map[string]any{}
	b, err := os.ReadFile(p.ApplyFile())
	switch {
	case errors.Is(err, os.ErrNotExist):
		if msg == "" {
			return nil
		}
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &m); err != nil {
			m = map[string]any{}
		}
	}
	if msg == "" {
		if _, ok := m["bootstrapWarning"]; !ok {
			return nil
		}
		delete(m, "bootstrapWarning")
	} else {
		m["bootstrapWarning"] = msg
	}
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return WriteAtomic(p.ApplyFile(), out)
}
