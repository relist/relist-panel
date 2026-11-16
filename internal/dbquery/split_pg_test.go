package dbquery

import (
	"strings"
	"testing"
)

func TestSplitPostgres(t *testing.T) {
	cases := []struct {
		name  string
		sql   string
		texts []string
	}{
		{"two", "select 1; select 2", []string{"select 1", "select 2"}},
		{"semicolon in a string", "select ';'; select 2", []string{"select ';'", "select 2"}},
		{"escape string", `select E'\';'; x`, []string{`select E'\';'`, "x"}},
		{"dollar body", "create function f() returns int as $$ select 1; $$ language sql; select 2",
			[]string{"create function f() returns int as $$ select 1; $$ language sql", "select 2"}},
		{"nested dollar tags", "$a$ $b$ ; $b$ $a$", []string{"$a$ $b$ ; $b$ $a$"}},
		{"nested comment", "/* a /* b; */ c; */ select 1", []string{"/* a /* b; */ c; */ select 1"}},
		{"line comment", "-- c;\nselect 1", []string{"-- c;\nselect 1"}},
		{"quoted identifier", `select "a;b"`, []string{`select "a;b"`}},
		{"empty statements dropped", "select 1;;  ;", []string{"select 1"}},
	}
	for _, c := range cases {
		got, err := SplitPostgres(c.sql)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		texts := make([]string, len(got))
		for i, st := range got {
			texts[i] = st.Text
		}
		if len(texts) != len(c.texts) {
			t.Errorf("%s: %q, want %q", c.name, texts, c.texts)
			continue
		}
		for i := range texts {
			if texts[i] != c.texts[i] {
				t.Errorf("%s[%d]: %q, want %q", c.name, i, texts[i], c.texts[i])
			}
		}
	}

	if _, err := SplitPostgres("select 'x"); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("unterminated quote = %v, want an error naming line 1", err)
	}

	got, err := SplitPostgres("select 1;\nselect 2")
	if err != nil || len(got) != 2 || got[0].Start != 0 || got[1].Start != 10 {
		t.Errorf("Start = %+v, %v, want 0 and 10", got, err)
	}
}
