package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// scriptRun is one run of release/install.sh with a fake docker first on PATH.
type scriptRun struct {
	code   int
	stdout string
	stderr string
	docker string // what the fake docker was called with
}

// shells lists the POSIX shells the script is tried under: sh, and dash when it
// is a different program.
func shells(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, name := range []string{"sh", "dash"} {
		if _, err := exec.LookPath(name); err == nil {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		t.Skip("no sh on this host")
	}
	return out
}

// runScript runs the install script. The fake docker records its arguments and
// fails, so a test learns whether the script reached Docker at all. noTTY runs
// the script in its own session, with no controlling terminal and stdin on
// /dev/null, like cron, CI or `ssh host 'curl ... | sh'`.
func runScript(t *testing.T, shell string, noTTY bool, env []string, args ...string) scriptRun {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "docker.log")
	fake := "#!/bin/sh\necho \"$@\" >> '" + log + "'\nexit 99\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, append([]string{"release/install.sh"}, args...)...)
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if noTTY {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	called, _ := os.ReadFile(log)
	return scriptRun{code: code, stdout: stdout.String(), stderr: stderr.String(), docker: string(called)}
}

func TestInstallScriptRejectsURLDomain(t *testing.T) {
	for _, shell := range shells(t) {
		for _, domain := range []string{
			"https://panel.example.com/",
			"panel.example.com/path",
			"panel.example.com:8443",
			"panel example.com",
			"panel.example.com\nEVIL=1",
			"localhost",
			"-bad.example.com",
		} {
			r := runScript(t, shell, true, nil, "--auto", "--domain", domain)
			if r.code != 2 || !strings.Contains(r.stderr, "host name only") {
				t.Errorf("%s %q: code %d stderr %q", shell, domain, r.code, r.stderr)
			}
			if r.docker != "" {
				t.Errorf("%s %q: docker was called: %q", shell, domain, r.docker)
			}
		}
	}
}

func TestInstallScriptRejectsUnknownFlag(t *testing.T) {
	for _, shell := range shells(t) {
		r := runScript(t, shell, true, nil, "--bogus")
		if r.code != 2 || !strings.Contains(r.stderr, "unknown option") || !strings.Contains(r.stderr, "Usage: install.sh") || r.docker != "" {
			t.Errorf("%s: code %d stderr %q docker %q", shell, r.code, r.stderr, r.docker)
		}
	}
}

func TestInstallScriptUsageErrors(t *testing.T) {
	for _, shell := range shells(t) {
		for name, args := range map[string][]string{
			"auto without domain": {"--auto"},
			"relative dir":        {"--auto", "--domain", "panel.example.com", "--dir", "x"},
			"missing value":       {"--domain"},
			"bad email":           {"--auto", "--domain", "panel.example.com", "--email", "not an email"},
			"bad acme email":      {"--auto", "--domain", "panel.example.com", "--acme-email", "a$b@c.d"},
		} {
			r := runScript(t, shell, true, nil, args...)
			if r.code != 2 || r.docker != "" {
				t.Errorf("%s %s: code %d stderr %q docker %q", shell, name, r.code, r.stderr, r.docker)
			}
		}
	}
}

func TestInstallScriptHelp(t *testing.T) {
	for _, shell := range shells(t) {
		r := runScript(t, shell, true, nil, "--help")
		if r.code != 0 || !strings.Contains(r.stdout, "--auto") || !strings.Contains(r.stdout, "--update") || r.docker != "" {
			t.Errorf("%s: code %d stdout %q", shell, r.code, r.stdout)
		}
	}
}

// Review focus: with no terminal (cron, CI, ssh host 'curl | sh') and no --auto
// the script must say to pass --auto, never wait on /dev/tty.
func TestInstallScriptNoTerminalNeedsAuto(t *testing.T) {
	for _, shell := range shells(t) {
		r := runScript(t, shell, true, nil)
		if r.code != 2 || !strings.Contains(r.stderr, "No terminal to ask on; pass --auto with --domain and --email") {
			t.Errorf("%s: code %d stderr %q", shell, r.code, r.stderr)
		}
		if r.docker != "" {
			t.Errorf("%s: docker was called: %q", shell, r.docker)
		}
		withDomain := runScript(t, shell, true, nil, "--domain", "panel.example.com", "--email", "me@example.com")
		if withDomain.code != 2 || !strings.Contains(withDomain.stderr, "pass --auto") {
			t.Errorf("%s with flags but no --auto: code %d stderr %q", shell, withDomain.code, withDomain.stderr)
		}
	}
}

func TestInstallScriptNeedsAReleaseImage(t *testing.T) {
	for _, shell := range shells(t) {
		r := runScript(t, shell, true, nil, "--auto", "--domain", "panel.example.com")
		if r.code != 1 || !strings.Contains(r.stderr, "no image filled in") || r.docker != "" {
			t.Errorf("%s: code %d stderr %q docker %q", shell, r.code, r.stderr, r.docker)
		}
	}
}

func TestInstallScriptUpdateNeedsAnInstallDir(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("--update checks root first; the dind probe covers it")
	}
	for _, shell := range shells(t) {
		r := runScript(t, shell, true, nil, "--update", "--dir", filepath.Join(t.TempDir(), "none"))
		if r.code == 0 {
			t.Errorf("%s: code 0", shell)
		}
	}
}
