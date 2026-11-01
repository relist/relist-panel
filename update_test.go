package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"relist-panel/internal/panelupdate"
)

const (
	testReport = "0123456789abcdef0123456789abcdef"
	testRun    = "20261101T120000Z"
)

type updateCalls struct {
	preflights int
	applies    []updateFlags
	report     *panelupdate.Report
	status     panelupdate.RunStatus
	err        error
}

func (c *updateCalls) ops(env map[string]string) updateOps {
	return updateOps{
		getenv: func(k string) string { return env[k] },
		preflight: func(_ context.Context, _ string, _ panelupdate.InstallArgs) (*panelupdate.Report, error) {
			c.preflights++
			return c.report, c.err
		},
		apply: func(_ context.Context, _ string, f updateFlags) (panelupdate.RunStatus, error) {
			c.applies = append(c.applies, f)
			return c.status, c.err
		},
	}
}

func runUpdateTest(t *testing.T, c *updateCalls, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = runUpdateWith(args, c.ops(env), &out, &errb)
	return code, out.String(), errb.String()
}

var dataEnv = map[string]string{"RELIST_DATA_DIR": "/var/lib/relist-panel"}

func TestUpdateUsage(t *testing.T) {
	cases := map[string][]string{
		"no mode":             {"--dir", "/x"},
		"both modes":          {"--preflight", "--report", testReport, "--run", testRun, "--dir", "/x"},
		"report without run":  {"--report", testReport, "--dir", "/x"},
		"bad run id":          {"--report", testReport, "--run", "../../etc", "--dir", "/x"},
		"bad report id":       {"--report", "../x", "--run", testRun, "--dir", "/x"},
		"run with preflight":  {"--preflight", "--run", testRun, "--dir", "/x"},
		"no dir":              {"--preflight"},
		"relative dir":        {"--preflight", "--dir", "x"},
		"extra argument":      {"--preflight", "--dir", "/x", "oops"},
		"unknown flag":        {"--preflight", "--dir", "/x", "--bogus"},
		"no arguments at all": nil,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			c := &updateCalls{report: &panelupdate.Report{}}
			code, _, stderr := runUpdateTest(t, c, dataEnv, args...)
			if code != 2 || !strings.Contains(stderr, "usage: relist-panel update") {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
			if c.preflights != 0 || len(c.applies) != 0 {
				t.Fatal("ran a mode after a usage error")
			}
		})
	}
	c := &updateCalls{}
	code, stdout, _ := runUpdateTest(t, c, dataEnv, "--help")
	if code != 0 || !strings.Contains(stdout, "usage: relist-panel update") {
		t.Fatalf("--help: code = %d, stdout = %q", code, stdout)
	}
}

func TestUpdateNeedsDataDir(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset":    {},
		"relative": {"RELIST_DATA_DIR": "data"},
	} {
		t.Run(name, func(t *testing.T) {
			c := &updateCalls{report: &panelupdate.Report{}}
			code, _, stderr := runUpdateTest(t, c, env, "--preflight", "--dir", "/x")
			if code != 2 || !strings.Contains(stderr, "RELIST_DATA_DIR") || c.preflights != 0 {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
		})
	}
}

func TestUpdatePreflightExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		r    panelupdate.Report
		want int
	}{
		"ready":      {panelupdate.Report{Ready: true}, 0},
		"not ready":  {panelupdate.Report{Failed: &panelupdate.Failure{Step: "pull", Message: "no"}}, 1},
		"up to date": {panelupdate.Report{UpToDate: true}, 3},
	} {
		t.Run(name, func(t *testing.T) {
			c := &updateCalls{report: &tc.r}
			code, _, _ := runUpdateTest(t, c, dataEnv, "--preflight", "--dir", "/x")
			if code != tc.want {
				t.Fatalf("code = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestUpdatePreflightJSON(t *testing.T) {
	c := &updateCalls{report: &panelupdate.Report{ID: testReport, Ready: true, ToVersion: "1.4.0"}}
	code, stdout, _ := runUpdateTest(t, c, dataEnv, "--preflight", "--json", "--dir", "/x")
	var got panelupdate.Report
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 || got.ID != testReport {
		t.Fatalf("code = %d, stdout = %q, err = %v", code, stdout, err)
	}
}

func TestUpdatePreflightText(t *testing.T) {
	c := &updateCalls{report: &panelupdate.Report{
		ID: testReport, Ready: false, FromVersion: "1.3.0", ToVersion: "1.4.0", Change: panelupdate.ChangeNotEdited,
		Invariants:  []panelupdate.InvariantResult{{Kind: "x", Text: "ok one", OK: true}, {Kind: "y", Text: "bad one", Detail: "missing", Source: "the Install override"}},
		ManualSteps: []panelupdate.ManualStep{{ID: "open-443", Text: "Open port 443."}},
	}}
	_, stdout, _ := runUpdateTest(t, c, dataEnv, "--preflight", "--dir", "/x")
	for _, want := range []string{"  ok    ok one", "  FAIL  bad one: missing (set by the Install override)", "Manual step [open-443]: Open port 443.", "Report: " + testReport} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestUpdateApplyExitCodes(t *testing.T) {
	args := []string{"--report", testReport, "--run", testRun, "--steps-done", "a, b", "--dir", "/x", "--project", "p", "--file", "/x/a.yml", "--file", "/x/b.yml"}
	c := &updateCalls{status: panelupdate.RunStatus{State: panelupdate.StateSucceeded, ToVersion: "1.4.0"}}
	code, stdout, _ := runUpdateTest(t, c, dataEnv, args...)
	if code != 0 || !strings.Contains(stdout, "Update succeeded") || len(c.applies) != 1 {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
	f := c.applies[0]
	if f.run != testRun || f.report != testReport || strings.Join(f.steps, "|") != "a|b" ||
		f.install.Project != "p" || strings.Join(f.install.Files, "|") != "/x/a.yml|/x/b.yml" {
		t.Fatalf("flags = %+v", f)
	}
	for _, state := range []string{panelupdate.StateRolledBack, panelupdate.StateAborted, panelupdate.StateRollbackFailed, panelupdate.StateInterrupted} {
		c := &updateCalls{status: panelupdate.RunStatus{State: state}}
		if code, _, _ := runUpdateTest(t, c, dataEnv, args...); code != 1 {
			t.Errorf("%s: code = %d, want 1", state, code)
		}
	}
	c = &updateCalls{err: errors.New("the run directory already exists")}
	if code, _, stderr := runUpdateTest(t, c, dataEnv, args...); code != 1 || !strings.Contains(stderr, "already exists") {
		t.Errorf("error: code = %d, stderr = %q", code, stderr)
	}
}

func TestUpdateDirFromFile(t *testing.T) {
	f, _, ok := parseUpdateFlags([]string{"--preflight", "--file", "/srv/relist/docker-compose.yml"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !ok || f.install.Dir != "/srv/relist" {
		t.Fatalf("flags = %+v, ok = %v", f, ok)
	}
}
