package dbquery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLSessionSQL(t *testing.T) {
	cases := []struct {
		engine string
		writes bool
		want   []string
	}{
		{"mysql", false, []string{"SET SESSION transaction_read_only=ON", "SET SESSION max_execution_time=30000"}},
		{"mysql", true, []string{"SET SESSION max_execution_time=30000"}},
		{"mariadb", false, []string{"SET SESSION transaction_read_only=ON", "SET SESSION max_statement_time=30"}},
		{"mariadb", true, []string{"SET SESSION max_statement_time=30"}},
	}
	for _, c := range cases {
		if got := mysqlSessionSQL(c.engine, c.writes); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s writes=%v = %q, want %q", c.engine, c.writes, got, c.want)
		}
	}
}

type fakeCursor struct {
	cols []Column
	rows [][]Cell
	i    int
	err  error
	sets int
}

func (c *fakeCursor) Columns() ([]Column, error) { return c.cols, nil }
func (c *fakeCursor) Next() bool                 { c.i++; return c.i <= len(c.rows) }
func (c *fakeCursor) Cells() ([]Cell, error)     { return c.rows[c.i-1], nil }
func (c *fakeCursor) NextResultSet() bool        { return false }
func (c *fakeCursor) Err() error                 { return c.err }
func (c *fakeCursor) Close() error               { return nil }

type fakeQueryer struct {
	script map[string]*fakeCursor
	fail   map[string]error
	log    []string
	errs   map[string]error
	ran    []string
}

func (f *fakeQueryer) query(_ context.Context, stmt string) (cursor, error) {
	f.log = append(f.log, stmt)
	if err := f.fail[stmt]; err != nil {
		return nil, err
	}
	f.ran = append(f.ran, stmt)
	if err := f.errs[stmt]; err != nil {
		return nil, err
	}
	if c, ok := f.script[stmt]; ok {
		return c, nil
	}
	return &fakeCursor{}, nil
}
func (f *fakeQueryer) rowCount(context.Context) (int64, error) { return 3, nil }

func TestMySQLErrorPosition(t *testing.T) {
	sql := "SELECT 1;\n  SELEC 2;\nSELECT 3"
	stmts, err := SplitMySQL(sql)
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueryer{
		script: map[string]*fakeCursor{"SELECT 1": {cols: []Column{{"1", "BIGINT"}}, rows: [][]Cell{{"1"}}}},
		errs:   map[string]error{"SELEC 2": &mysql.MySQLError{Number: 1064, Message: "You have an error in your SQL syntax"}},
	}
	resp, _ := runBatch(context.Background(), q, stmts, func() {})
	if resp.Error == nil || resp.Error.StatementIndex != 1 || resp.Error.StatementStart != stmts[1].Start || resp.Error.Position != nil {
		t.Fatalf("error = %+v, want statement 1 starting at %d", resp.Error, stmts[1].Start)
	}
	if resp.Error.StatementStart != 12 {
		t.Errorf("start = %d, want 12 (after the newline and two spaces)", resp.Error.StatementStart)
	}
	if len(resp.Results) != 1 || len(q.ran) != 2 {
		t.Errorf("the batch must stop at the error: results %d ran %q", len(resp.Results), q.ran)
	}
}

func TestMySQLNoColumnsGivesAffected(t *testing.T) {
	stmts, _ := SplitMySQL("UPDATE t SET a=1")
	resp, _ := runBatch(context.Background(), &fakeQueryer{}, stmts, func() {})
	if len(resp.Results) != 1 || resp.Results[0].Command != "OK" || resp.Results[0].Affected == nil || *resp.Results[0].Affected != 3 {
		t.Fatalf("results = %+v", resp.Results)
	}
}

func TestMySQLRowCapStopsTheBatch(t *testing.T) {
	rows := make([][]Cell, 1001)
	for i := range rows {
		rows[i] = []Cell{"x"}
	}
	stmts, _ := SplitMySQL("SELECT * FROM big; SELECT 2")
	stopped := false
	q := &fakeQueryer{script: map[string]*fakeCursor{"SELECT * FROM big": {cols: []Column{{"a", "INT"}}, rows: rows}}}
	resp, _ := runBatch(context.Background(), q, stmts, func() { stopped = true })
	if !stopped || !resp.Results[0].Truncated || len(resp.Results[0].Rows) != 1000 ||
		resp.Stopped != "Stopped after 1,000 rows. Statements after it did not run." {
		t.Fatalf("stopped=%v resp=%+v", stopped, resp.Stopped)
	}
	_ = errors.New
}

// The server's error number travels as the error's code: the audit log keeps
// it instead of the message, which may quote the statement.
func TestMySQLErrorCode(t *testing.T) {
	stmts, _ := SplitMySQL("SELEC 2")
	q := &fakeQueryer{errs: map[string]error{"SELEC 2": &mysql.MySQLError{Number: 1064, Message: "near 'SELEC 2'"}}}
	resp, _ := runBatch(context.Background(), q, stmts, func() {})
	if resp.Error == nil || resp.Error.Code != "1064" {
		t.Fatalf("error = %+v, want code 1064", resp.Error)
	}
}
