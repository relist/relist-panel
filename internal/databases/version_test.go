package databases

import (
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
