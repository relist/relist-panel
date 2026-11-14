package databases

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckUpload(t *testing.T) {
	copyData := "COPY public.t (s) FROM stdin;\nCREATE DATABASE x\n\\.\n"
	cases := []struct {
		name, engine, file string
		line               int // 0: accepted
	}{
		{"plain pg_dump", "postgres", "\\restrict abc\nSET x = 1;\nCREATE TABLE t (id int);\n", 0},
		{"pg_dumpall", "postgres", "SET x = 1;\n\nCREATE ROLE postgres;\n", 3},
		{"connect", "postgres", "\\connect app\n", 1},
		{"shell", "postgres", "\\! id\n", 1},
		{"copy data", "postgres", copyData, 0},
		{"after copy", "postgres", copyData + "DROP DATABASE y;\n", 4},
		{"create schema", "postgres", "CREATE SCHEMA app;\n", 0},
		{"if exists", "postgres", "DROP ROLE IF EXISTS app;\n", 1},
		{"use", "mysql", "USE `shop`;\n", 1},
		{"add-drop-database", "mysql", "SET x = 1;\n/*!40000 DROP DATABASE IF EXISTS `shop`*/;\n", 2},
		{"create schema", "mysql", "CREATE SCHEMA shop;\n", 1},
		{"alter user", "mariadb", "ALTER USER root IDENTIFIED BY 'x';\n", 1},
		{"plain mysqldump", "mysql", "CREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n", 0},
		{"set password", "mysql", "SET PASSWORD FOR root = 'x';\n", 1},
		{"indented and lower case", "postgres", "  create database x;\n", 1},
		{"unrestrict", "postgres", "\\unrestrict abc\n", 0},
		{"windows newlines in copy", "postgres", "COPY t FROM stdin;\r\nCREATE ROLE x\r\n\\.\r\nSELECT 1;\r\n", 0},
	}
	for _, c := range cases {
		t.Run(c.engine+" "+c.name, func(t *testing.T) {
			err := CheckUpload(c.engine, strings.NewReader(c.file))
			if c.line == 0 {
				if err != nil {
					t.Fatalf("err = %v, want ok", err)
				}
				return
			}
			var f ErrForbiddenStatement
			if !errors.As(err, &f) || f.Line != c.line {
				t.Fatalf("err = %v, want ErrForbiddenStatement at line %d", err, c.line)
			}
		})
	}
}

func TestCheckUploadMessageAndLongLines(t *testing.T) {
	err := CheckUpload("postgres", strings.NewReader("CREATE ROLE "+strings.Repeat("x", 500)+";\n"))
	var f ErrForbiddenStatement
	if !errors.As(err, &f) || len(f.Text) > 80 || !strings.HasPrefix(err.Error(), "line 1: \"CREATE ROLE x") ||
		!strings.Contains(err.Error(), "is not allowed in an upload; dump one database with pg_dump <db> or mysqldump <db>") {
		t.Fatalf("err = %v", err)
	}
	// A multi-megabyte data line is fine, and line numbers stay right after it.
	big := "INSERT INTO t VALUES ('" + strings.Repeat("a", 3<<20) + "');\nUSE x;\n"
	if err := CheckUpload("mysql", strings.NewReader(big)); !errors.As(err, &f) || f.Line != 2 {
		t.Fatalf("err = %v, want line 2", err)
	}
}

func TestCheckTarget(t *testing.T) {
	if r, err := checkTarget("app", []string{"app"}); err != nil || !r {
		t.Fatalf("existing = %v, %v", r, err)
	}
	if r, err := checkTarget("new_db", []string{"app"}); err != nil || r {
		t.Fatalf("new = %v, %v", r, err)
	}
	for _, bad := range []string{"template1", "template0", "mysql", "sys", "information_schema", "performance_schema", "-x", "", "a b", strings.Repeat("a", 64)} {
		if _, err := checkTarget(bad, []string{"app"}); !errors.Is(err, ErrBadTarget) {
			t.Errorf("%q: err = %v, want ErrBadTarget", bad, err)
		}
	}
	if _, err := checkTarget(strings.Repeat("a", 63), nil); err != nil {
		t.Fatalf("63 chars: %v", err)
	}
}
