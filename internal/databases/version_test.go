package databases

import (
	"encoding/hex"
	"slices"
	"strings"
	"testing"
)

func TestCompareTags(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"16", "18", -1}, {"18", "16", 1}, {"7.4", "8", -1}, {"11.4", "11.8", -1},
		{"11.8", "11.8", 0}, {"8.4", "8.4.1", -1}, {"8.4.0", "8.4", 0},
	} {
		if got := compareTags(c.a, c.b); got != c.want {
			t.Errorf("compareTags(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestHigherVersions(t *testing.T) {
	for _, c := range []struct {
		engine, current string
		want            []string
	}{
		{"postgres", "16", []string{"17", "18"}},
		{"postgres", "18", []string{}},
		{"mariadb", "11.4", []string{"11.8"}},
		{"redis", "7.4", []string{"8"}},
		{"mysql", "8.4", []string{}},
	} {
		got := HigherVersions(c.engine, c.current)
		if got == nil || !slices.Equal(got, c.want) {
			t.Errorf("HigherVersions(%s, %s) = %#v, want %#v", c.engine, c.current, got, c.want)
		}
	}
}

// rolesDump is pg_dumpall -U postgres --roles-only from postgres 16.16
// (captured in the sandbox 2026-11-17), with the SCRAM hashes replaced.
const rolesDump = `--
-- PostgreSQL database cluster dump
--

\restrict sMgavOZBYucbYJXpoATLxZKbt4o1mopYJapWRsaBQAhZc3HBZdYXYEI75Luilp2

SET default_transaction_read_only = off;

SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;

--
-- Roles
--

CREATE ROLE app;
ALTER ROLE app WITH NOSUPERUSER INHERIT NOCREATEROLE NOCREATEDB LOGIN NOREPLICATION NOBYPASSRLS PASSWORD 'SCRAM-SHA-256$4096:fake-app';
CREATE ROLE full_u;
ALTER ROLE full_u WITH NOSUPERUSER INHERIT NOCREATEROLE NOCREATEDB LOGIN NOREPLICATION NOBYPASSRLS PASSWORD 'SCRAM-SHA-256$4096:fake-full';
CREATE ROLE postgres;
ALTER ROLE postgres WITH SUPERUSER INHERIT CREATEROLE CREATEDB LOGIN REPLICATION BYPASSRLS PASSWORD 'SCRAM-SHA-256$4096:fake-postgres';

--
-- User Configurations
--

--
-- User Config "app"
--

ALTER ROLE app SET statement_timeout TO '5s';


--
-- Role memberships
--

GRANT app TO full_u WITH INHERIT TRUE GRANTED BY postgres;




\unrestrict sMgavOZBYucbYJXpoATLxZKbt4o1mopYJapWRsaBQAhZc3HBZdYXYEI75Luilp2

--
-- PostgreSQL database cluster dump complete
--

`

func TestFilterRoles(t *testing.T) {
	got := filterRoles(rolesDump)
	if strings.Contains(got, "CREATE ROLE postgres;") || strings.Contains(got, "ALTER ROLE postgres WITH") {
		t.Fatalf("postgres's own role lines kept:\n%s", got)
	}
	in, out := strings.Split(rolesDump, "\n"), strings.Split(got, "\n")
	if len(out) != len(in)-2 {
		t.Fatalf("lines %d -> %d, want exactly 2 fewer", len(in), len(out))
	}
	var kept []string
	for _, l := range in {
		if l != "CREATE ROLE postgres;" && !strings.HasPrefix(l, "ALTER ROLE postgres WITH ") {
			kept = append(kept, l)
		}
	}
	if !slices.Equal(out, kept) {
		t.Fatalf("other lines changed:\n%s", got)
	}
	for _, want := range []string{`\restrict sMgav`, `\unrestrict sMgav`, "GRANT app TO full_u WITH INHERIT TRUE GRANTED BY postgres;", "CREATE ROLE full_u;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestRedactPasswords(t *testing.T) {
	in := "ERROR:  syntax error\nLINE 1: ALTER ROLE app WITH LOGIN PASSWORD 'SCRAM-SHA-256$4096:abc''d' VALID UNTIL 'infinity';\n" +
		"LINE 2: create role b password 'plain'"
	got := redactPasswords(in)
	if !strings.Contains(got, "PASSWORD '***'") || !strings.Contains(got, "password '***'") {
		t.Fatalf("not redacted: %q", got)
	}
	for _, leak := range []string{"SCRAM", "abc", "''d", "plain"} {
		if strings.Contains(got, leak) {
			t.Fatalf("%q still in %q", leak, got)
		}
	}
	if !strings.Contains(got, "VALID UNTIL 'infinity'") {
		t.Fatalf("redaction ran past the password: %q", got)
	}
}

func TestDBSettingsQueryQuotes(t *testing.T) {
	q := dbSettingsQuery("my'db")
	if !strings.Contains(q, "'my''db'") {
		t.Fatalf("query does not quote the name: %s", q)
	}
}

// Review: psql cuts a long LINE echo with "...", so the closing quote can be
// missing; the hash must still go.
func TestRedactPasswordsTruncated(t *testing.T) {
	in := "ERROR:  boom\nLINE 1: ...LOGIN NOREPLICATION NOBYPASSRLS PASSWORD 'SCRAM-SHA-256$4096:abcdefsalt...\n                                                             ^\nnext line"
	got := redactPasswords(in)
	for _, leak := range []string{"SCRAM", "abcdef"} {
		if strings.Contains(got, leak) {
			t.Fatalf("%q still in %q", leak, got)
		}
	}
	if !strings.Contains(got, "next line") || !strings.Contains(got, "PASSWORD '***'") {
		t.Fatalf("redacted too much or too little: %q", got)
	}
}

// settingsRow is one line of dbSettingsQuery's output: the role (empty for a
// database-wide setting) and one setconfig element, each hex-encoded.
func settingsRow(role, cfg string) string {
	return hex.EncodeToString([]byte(role)) + "|" + hex.EncodeToString([]byte(cfg)) + "\n"
}

// Security review: a list setting stored with SET … FROM CURRENT is the
// session's raw text, never quoted by the server; replayed as SQL text it ran
// as the superuser. Every value now goes back as literals.
func TestSettingsSQLQuotesEveryValue(t *testing.T) {
	for _, c := range []struct{ role, cfg, want string }{
		{"app1", "search_path=x;ALTER/**/ROLE/**/app1/**/SUPERUSER;--",
			`ALTER ROLE "app1" IN DATABASE "app" SET "search_path" TO 'x;ALTER/**/ROLE/**/app1/**/SUPERUSER;--';`},
		{"app1", `search_path="$user", public`, `ALTER ROLE "app1" IN DATABASE "app" SET "search_path" TO '$user', 'public';`},
		{"", `search_path="a""b",  c`, `ALTER DATABASE "app" SET "search_path" TO 'a"b', 'c';`},
		{"", `search_path=""`, `ALTER DATABASE "app" SET "search_path" TO '';`},
		{"", `search_path="unclosed`, `ALTER DATABASE "app" SET "search_path" TO '"unclosed';`},
		// Review: a trailing separator ran past the end of the value and
		// panicked the whole panel; local_preload_libraries takes it.
		{"", "local_preload_libraries=a,", `ALTER DATABASE "app" SET "local_preload_libraries" TO 'a,';`},
		{"", "local_preload_libraries=a, ", `ALTER DATABASE "app" SET "local_preload_libraries" TO 'a, ';`},
		{"", `local_preload_libraries="a",`, `ALTER DATABASE "app" SET "local_preload_libraries" TO '"a",';`},
		{"", "local_preload_libraries=a,\v", "ALTER DATABASE \"app\" SET \"local_preload_libraries\" TO 'a,\v';"},
		{"", `local_preload_libraries=a';DROP ROLE x;--,b`, `ALTER DATABASE "app" SET "local_preload_libraries" TO 'a'';DROP ROLE x;--,b';`},
		{"full_u", "role=app_owner", `ALTER ROLE "full_u" IN DATABASE "app" SET "role" TO 'app_owner';`},
		{"", "work_mem=8MB", `ALTER DATABASE "app" SET "work_mem" TO '8MB';`},
		{`a"b`, "application_name=it's=1", `ALTER ROLE "a""b" IN DATABASE "app" SET "application_name" TO 'it''s=1';`},
	} {
		got, err := settingsSQL("app", settingsRow(c.role, c.cfg))
		if err != nil {
			t.Fatalf("%s: %v", c.cfg, err)
		}
		if strings.TrimSpace(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.cfg, strings.TrimSpace(got), c.want)
		}
	}
}

func TestSettingsSQLRefusesGarbage(t *testing.T) {
	for _, out := range []string{"zz|00\n", "6170700a\n", "|" + hex.EncodeToString([]byte("no_equals")) + "\n"} {
		if _, err := settingsSQL("app", out); err == nil {
			t.Errorf("%q: no error", out)
		}
	}
	if got, err := settingsSQL("app", "\n"); err != nil || got != "" {
		t.Fatalf("empty = %q, %v", got, err)
	}
}

// Every string of up to six characters from the separators, quotes,
// whitespace and a name character splits or fails, never panics, and what
// it splits into joins back without a stray separator.
func TestSplitGUCListNeverPanics(t *testing.T) {
	alphabet := []string{"a", ",", " ", `"`, "\v"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		names, ok := splitGUCList(prefix)
		if ok {
			for _, n := range names {
				if n == "" && !strings.Contains(prefix, `""`) {
					t.Fatalf("%q split into an empty unquoted name: %q", prefix, names)
				}
			}
		}
		if depth == 0 {
			return
		}
		for _, c := range alphabet {
			walk(prefix+c, depth-1)
		}
	}
	walk("", 6)
}
