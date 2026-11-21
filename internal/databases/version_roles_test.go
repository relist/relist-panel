package databases

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roleSettingRow is one line of roleSettingsSQL's output.
func roleSettingRow(role, cfg string) string {
	return hex.EncodeToString([]byte(role)) + "|" + hex.EncodeToString([]byte(cfg)) + "\n"
}

func TestUnsplittableRoleSetting(t *testing.T) {
	cases := []struct {
		name, out      string
		role, setting  string
		wantParseError bool
	}{
		{name: "none", out: ""},
		{name: "a plain value", out: roleSettingRow("app", "work_mem=64MB")},
		{name: "a list that splits", out: roleSettingRow("app", `search_path="$user", public`)},
		{name: "a value that only looks like a list", out: roleSettingRow("app", "application_name=a,")},
		{name: "a trailing comma", out: roleSettingRow("app", "work_mem=1MB") + roleSettingRow("Odd Role", "local_preload_libraries=a,"),
			role: "Odd Role", setting: "local_preload_libraries"},
		{name: "an empty element", out: roleSettingRow("app", "temp_tablespaces=a,,b"), role: "app", setting: "temp_tablespaces"},
		{name: "an unclosed quote", out: roleSettingRow("app", `search_path="x`), role: "app", setting: "search_path"},
		// No names at all: pg_dumpall writes "SET search_path TO ;" (checked on 16).
		{name: "an empty list", out: roleSettingRow("app", "search_path="), role: "app", setting: "search_path"},
		{name: "a blank list", out: roleSettingRow("app", "temp_tablespaces=  "), role: "app", setting: "temp_tablespaces"},
		{name: "an empty quoted name", out: roleSettingRow("app", `search_path=""`)},
		{name: "not hex", out: "zz|00\n", wantParseError: true},
		{name: "no separator", out: "6162\n", wantParseError: true},
	}
	for _, c := range cases {
		bad, err := unsplittableRoleSetting(c.out)
		if c.wantParseError {
			if err == nil {
				t.Errorf("%s: no parse error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		switch {
		case c.role == "" && bad != nil:
			t.Errorf("%s: refused %+v", c.name, *bad)
		case c.role != "" && (bad == nil || bad.Role != c.role || bad.Setting != c.setting):
			t.Errorf("%s: got %+v, want %s %s", c.name, bad, c.role, c.setting)
		}
	}
}

// A role-wide list setting pg_dump cannot split makes pg_dumpall write
// "SET … TO ;", and the roles restore of a Change version would fail. It is
// refused up front, before any client is cut off, with the fix named.
func TestChangeVersionRefusesAnUnsplittableRoleSetting(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	r.host.override(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		if strings.Contains(string(b), "setdatabase = 0") {
			_, _ = io.WriteString(req.Stdout, roleSettingRow("app_owner", "local_preload_libraries=a,"))
		}
		return 0, nil
	})
	_, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false)
	var bad ErrUnsplittableSetting
	if !errors.As(err, &bad) || bad.Role != "app_owner" || bad.Setting != "local_preload_libraries" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), `ALTER ROLE "app_owner" RESET local_preload_libraries`) {
		t.Fatalf("message does not name the fix: %v", err)
	}
	if list, err := r.store.ListVersionChanges(ctx, r.d.ID); err != nil || len(list) != 0 {
		t.Fatalf("version changes = %+v, %v", list, err)
	}
	if job := r.svc.Jobs(r.d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
	// Only the read of the settings ran: nothing pulled, cut off or stopped.
	for _, call := range r.host.mutating() {
		if !strings.HasSuffix(call, pgAdmin) {
			t.Fatalf("host changed before the refusal: %v", r.host.mutating())
		}
	}
}

// The server failing to answer the settings read is the server's answer,
// not a fault of the panel: a bad gateway, scrubbed, like other admin reads.
func TestRoleSettingsReadFailureIsABadGateway(t *testing.T) {
	r := newPGChangeRig(t)
	ctx := context.Background()
	r.host.override(pgAdmin, func(req ExecRequest) (int, error) {
		b, _ := io.ReadAll(req.Stdin)
		if strings.Contains(string(b), "setdatabase = 0") {
			_, _ = io.WriteString(req.Stderr, "FATAL:  the database system is starting up")
			return 2, nil
		}
		return 0, nil
	})
	_, err := r.svc.ChangeVersion(ctx, r.d.ID, "18", false)
	var admin adminError
	if !errors.As(err, &admin) || !strings.Contains(err.Error(), "starting up") {
		t.Fatalf("err = %v", err)
	}
	w := httptest.NewRecorder()
	writeError(w, err)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", w.Code)
	}
	if job := r.svc.Jobs(r.d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
}
