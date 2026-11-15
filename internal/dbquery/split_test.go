package dbquery

import (
	"strings"
	"testing"
)

func TestSplitMySQL(t *testing.T) {
	proc := "DELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END//\nDELIMITER ;\nCALL p();"
	cases := []struct {
		name, in string
		want     []string
		errLine  string
	}{
		{"two", "SELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}, ""},
		{"quotes and comment", "SELECT ';'; SELECT \"a;b\"; SELECT 1 -- ;x\n;", []string{"SELECT ';'", "SELECT \"a;b\"", "SELECT 1 -- ;x"}, ""},
		{"backticks", "SELECT `a;b` FROM t", []string{"SELECT `a;b` FROM t"}, ""},
		{"versioned comment", "/*! SET x=1 */; SELECT 1", []string{"/*! SET x=1 */", "SELECT 1"}, ""},
		{"hash comment", "# c;\nSELECT 1", []string{"# c;\nSELECT 1"}, ""},
		{"delimiter", proc, []string{"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END", "CALL p()"}, ""},
		{"backslash escape", `SELECT 'it\'s'; SELECT 2`, []string{`SELECT 'it\'s'`, "SELECT 2"}, ""},
		{"doubled quote", "SELECT 'it''s;'; SELECT 2", []string{"SELECT 'it''s;'", "SELECT 2"}, ""},
		{"block comment skipped", "SELECT 1 /* ; */; SELECT 2", []string{"SELECT 1", "SELECT 2"}, ""},
		{"empty dropped", ";;  ; SELECT 1;;", []string{"SELECT 1"}, ""},
		{"unterminated quote", "SELECT 1;\nSELECT 'open", nil, "line 2"},
		{"unterminated comment", "SELECT 1 /* open", nil, "line 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SplitMySQL(c.in)
			if c.errLine != "" {
				if err == nil || !strings.Contains(err.Error(), c.errLine) {
					t.Fatalf("err = %v, want %q", err, c.errLine)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d statements %v, want %d %q", len(got), got, len(c.want), c.want)
			}
			runes := []rune(c.in)
			for i, s := range got {
				if s.Text != c.want[i] {
					t.Errorf("stmt %d = %q, want %q", i, s.Text, c.want[i])
				}
				if s.Start < 0 || s.Start >= len(runes) || runes[s.Start] != []rune(s.Text)[0] {
					t.Errorf("stmt %d Start %d does not point at %q", i, s.Start, s.Text[:1])
				}
			}
		})
	}
}

func TestSplitMySQLStartAfterMultibyte(t *testing.T) {
	got, err := SplitMySQL("SELECT 'é'; SELECT 2")
	if err != nil || len(got) != 2 || got[1].Start != 12 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
