package dbquery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPreviewStatements(t *testing.T) {
	allowed := []struct{ engine, sql string }{
		{"postgres", "-- note\nDELETE FROM t"},
		{"postgres", "/* c */ update t set a=1"},
		{"postgres", "INSERT INTO t VALUES (1); DELETE FROM u"},
		{"mysql", "REPLACE INTO t VALUES (1)"},
	}
	for _, c := range allowed {
		if stmts, qe := PreviewStatements(c.engine, c.sql); qe != nil || len(stmts) == 0 {
			t.Errorf("%s %q: %+v, want allowed", c.engine, c.sql, qe)
		}
	}
	refused := []struct {
		engine, sql string
		index       int
		reason      string
	}{
		{"postgres", "REPLACE INTO t VALUES (1)", 0, "Statement 1 starts with REPLACE."},
		{"postgres", "DELETE FROM t; COMMIT", 1, "Statement 2 starts with COMMIT."},
		{"postgres", "WITH x AS (SELECT 1) DELETE FROM t", 0, "Statement 1 starts with WITH."},
		{"postgres", "TRUNCATE t", 0, "Statement 1 starts with TRUNCATE."},
		{"postgres", "SELECT 1", 0, "Statement 1 starts with SELECT."},
		{"mysql", "/*!50000 DROP TABLE t */", 0, "starts with an executable comment."},
		{"mariadb", "/*M!100000 DROP TABLE t */", 0, "starts with an executable comment."},
	}
	for _, c := range refused {
		stmts, qe := PreviewStatements(c.engine, c.sql)
		if qe == nil {
			t.Errorf("%s %q: allowed (%d statements), want refused", c.engine, c.sql, len(stmts))
			continue
		}
		if qe.StatementIndex != c.index || !strings.Contains(qe.Message, c.reason) {
			t.Errorf("%s %q: %+v, want index %d reason %q", c.engine, c.sql, qe, c.index, c.reason)
		}
		base := "INSERT, UPDATE and DELETE"
		if c.engine != "postgres" {
			base = "INSERT, UPDATE, DELETE and REPLACE"
		}
		if !strings.Contains(qe.Message, base) {
			t.Errorf("%s %q: message %q is missing %q", c.engine, c.sql, qe.Message, base)
		}
	}
	if _, qe := PreviewStatements("postgres", "DELETE FROM t; COMMIT"); qe == nil || qe.StatementStart != 15 {
		t.Errorf("StatementStart = %+v, want 15", qe)
	}
	if _, qe := PreviewStatements("postgres", "  "); qe == nil || !strings.Contains(qe.Message, "there is nothing to run") {
		t.Errorf("empty = %+v", qe)
	}
}

func TestPreviewTarget(t *testing.T) {
	cases := []struct{ sql, verb, table string }{
		{`DELETE FROM "Shop".orders WHERE id = 1`, "delete", "Shop.orders"},
		{"INSERT IGNORE INTO `logs` VALUES (1)", "insert", "logs"},
		{"UPDATE ONLY t SET a = 1", "update", "t"},
		{"DELETE t1, t2 FROM t1 JOIN t2", "delete", ""},
	}
	for _, c := range cases {
		verb, table := PreviewTarget(c.sql)
		if verb != c.verb || table != c.table {
			t.Errorf("%q: %q %q, want %q %q", c.sql, verb, table, c.verb, c.table)
		}
	}
}

func TestMySQLPreviewRollsBack(t *testing.T) {
	ctx := context.Background()
	stmts, qe := PreviewStatements("mysql", "INSERT INTO t VALUES (1); DELETE FROM u")
	if qe != nil {
		t.Fatal(qe)
	}
	q := &fakeQueryer{}
	resp, _ := runPreviewBatch(ctx, q, stmts, func() {})
	want := []string{"START TRANSACTION", "INSERT INTO t VALUES (1)", "DELETE FROM u", "ROLLBACK"}
	if !reflect.DeepEqual(q.log, want) {
		t.Fatalf("sent %v, want %v", q.log, want)
	}
	if !resp.Preview || resp.Error != nil || len(resp.Results) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Results[0].Verb != "insert" || resp.Results[0].Target != "t" || resp.Results[1].Verb != "delete" || resp.Results[1].Target != "u" {
		t.Fatalf("verbs = %+v", resp.Results)
	}

	// ROLLBACK also follows a failing statement.
	q = &fakeQueryer{fail: map[string]error{"DELETE FROM u": errors.New("boom")}}
	resp, _ = runPreviewBatch(ctx, q, stmts, func() {})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "boom") {
		t.Fatalf("resp = %+v", resp)
	}
	if n := len(q.log); n == 0 || q.log[n-1] != "ROLLBACK" {
		t.Fatalf("sent %v, want a trailing ROLLBACK", q.log)
	}
}
