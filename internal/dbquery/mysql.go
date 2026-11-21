package dbquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Server errors that mean the statement ran out of time.
const (
	mysqlErrQueryTimeout   = 3024 // MySQL: max_execution_time
	mariadbErrQueryTimeout = 1969 // MariaDB: max_statement_time
)

// mysqlSessionSQL is what a run sets on its own connection, in order.
func mysqlSessionSQL(engine string, writes bool) []string {
	var out []string
	if !writes {
		out = append(out, "SET SESSION transaction_read_only=ON")
	}
	if engine == "mariadb" {
		return append(out, "SET SESSION max_statement_time=30")
	}
	return append(out, "SET SESSION max_execution_time=30000")
}

// cursor is one statement's results, read set by set.
type cursor interface {
	Columns() ([]Column, error)
	Next() bool
	Cells() ([]Cell, error)
	NextResultSet() bool
	Err() error
	Close() error
}

// queryer is the one connection a batch runs on.
type queryer interface {
	query(ctx context.Context, stmt string) (cursor, error)
	rowCount(ctx context.Context) (int64, error)
}

func firstWord(stmt string) string {
	for _, f := range strings.FieldsFunc(stmt, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' }) {
		return strings.ToUpper(f)
	}
	return "OK"
}

// runBatch runs the statements one after another, stopping at the first
// error or limit. stop is called when a limit is reached: it must end what
// the server is running. The second result is the server's error number, 0
// when the error was not the server's.
func runBatch(ctx context.Context, q queryer, stmts []Statement, stop func()) (QueryResponse, uint16) {
	began := time.Now()
	resp := QueryResponse{Results: []QueryResult{}}
	var budget Budget
	fail := func(i int, err error) (QueryResponse, uint16) {
		qe := &QueryError{Message: err.Error(), StatementIndex: i, StatementStart: stmts[i].Start}
		var num uint16
		var me *mysql.MySQLError
		if errors.As(err, &me) {
			qe.Message, num = me.Message, me.Number
		}
		resp.Error = qe
		resp.DurationMs = sinceMs(began)
		return resp, num
	}
	stopped := func(text string, remaining int) (QueryResponse, uint16) {
		stop()
		resp.Stopped = text
		if remaining > 0 {
			resp.Stopped += " Statements after it did not run."
		}
		resp.DurationMs = sinceMs(began)
		return resp, 0
	}
	for i, st := range stmts {
		start := time.Now()
		cur, err := q.query(ctx, st.Text)
		if err != nil {
			return fail(i, err)
		}
		first := true
		for {
			cols, err := cur.Columns()
			if err != nil {
				_ = cur.Close()
				return fail(i, err)
			}
			if len(cols) == 0 {
				if first {
					// No result set: an OK packet. ROW_COUNT() is the one place
					// its affected rows are readable.
					_ = cur.Close()
					n, err := q.rowCount(ctx)
					if err != nil {
						return fail(i, err)
					}
					if !budget.Result() {
						return stopped(budget.Stopped(), len(stmts)-i)
					}
					resp.Results = append(resp.Results, QueryResult{StatementIndex: i, Columns: []Column{}, Rows: [][]Cell{}, Command: "OK", Affected: &n, DurationMs: sinceMs(start)})
					break
				}
			} else {
				if !budget.Result() {
					_ = cur.Close()
					return stopped(budget.Stopped(), len(stmts)-i)
				}
				res := QueryResult{StatementIndex: i, Columns: cols, Rows: [][]Cell{}}
				for cur.Next() {
					row, err := cur.Cells()
					if err != nil {
						_ = cur.Close()
						return fail(i, err)
					}
					if !budget.Row(row) {
						res.Truncated = true
						res.Command = firstWord(st.Text) + " " + strconv.Itoa(len(res.Rows))
						res.DurationMs = sinceMs(start)
						resp.Results = append(resp.Results, res)
						stop()
						_ = cur.Close()
						return stopped(budget.Stopped(), len(stmts)-i-1)
					}
					res.Rows = append(res.Rows, row)
				}
				if err := cur.Err(); err != nil {
					_ = cur.Close()
					return fail(i, err)
				}
				res.Command = firstWord(st.Text) + " " + strconv.Itoa(len(res.Rows))
				res.DurationMs = sinceMs(start)
				resp.Results = append(resp.Results, res)
			}
			first = false
			if !cur.NextResultSet() {
				break
			}
		}
		if err := cur.Err(); err != nil {
			_ = cur.Close()
			return fail(i, err)
		}
		_ = cur.Close()
	}
	resp.DurationMs = sinceMs(began)
	return resp, 0
}

// sqlConn is a queryer over one *sql.Conn.
type sqlConn struct{ c *sql.Conn }

type sqlCursor struct {
	rows *sql.Rows
	dest []any
}

func (s sqlConn) query(ctx context.Context, stmt string) (cursor, error) {
	rows, err := s.c.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	return &sqlCursor{rows: rows}, nil
}

func (s sqlConn) rowCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.c.QueryRowContext(ctx, "SELECT ROW_COUNT()").Scan(&n)
	return n, err
}

func (c *sqlCursor) Columns() ([]Column, error) {
	types, err := c.rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := make([]Column, len(types))
	for i, t := range types {
		out[i] = Column{Name: t.Name(), Type: t.DatabaseTypeName()}
	}
	c.dest = make([]any, len(types))
	return out, nil
}

func (c *sqlCursor) Next() bool          { return c.rows.Next() }
func (c *sqlCursor) NextResultSet() bool { return c.rows.NextResultSet() }
func (c *sqlCursor) Err() error          { return c.rows.Err() }
func (c *sqlCursor) Close() error        { return c.rows.Close() }

// Cells reads one row. Scanning into any keeps NULL (nil) apart from an
// empty string; the driver sends integers and floats as Go numbers, which
// are formatted with every digit.
func (c *sqlCursor) Cells() ([]Cell, error) {
	ptrs := make([]any, len(c.dest))
	for i := range ptrs {
		ptrs[i] = &c.dest[i]
	}
	if err := c.rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]Cell, len(c.dest))
	for i, v := range c.dest {
		switch x := v.(type) {
		case nil:
			out[i] = nil
		case []byte:
			out[i] = ShapeCell(x, false)
		case string:
			out[i] = ShapeCell([]byte(x), false)
		case int64:
			out[i] = strconv.FormatInt(x, 10)
		case uint64:
			out[i] = strconv.FormatUint(x, 10)
		case float32:
			out[i] = strconv.FormatFloat(float64(x), 'g', -1, 32)
		case float64:
			out[i] = strconv.FormatFloat(x, 'g', -1, 64)
		case time.Time:
			out[i] = x.Format("2006-01-02 15:04:05.999999")
		default:
			out[i] = ShapeCell([]byte(fmt.Sprint(x)), false)
		}
	}
	return out, nil
}

func mysqlConfig(t Target, database string) *mysql.Config {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = t.User, t.Password
	cfg.Net, cfg.Addr = "tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	cfg.DBName = database
	cfg.Timeout = 5 * time.Second
	cfg.MultiStatements = false
	return cfg
}

func openConn(ctx context.Context, cfg *mysql.Config) (*sql.DB, *sql.Conn, error) {
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return db, conn, nil
}

// RunMySQL runs r.SQL statement by statement on one connection of its own.
// When the context ends or a limit is reached, a second connection sends
// KILL QUERY: the driver only drops its socket, which leaves the statement
// running on the server.
func RunMySQL(ctx context.Context, t Target, r Request) QueryResponse {
	began := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	failed := func(err error) QueryResponse {
		resp := QueryResponse{Results: []QueryResult{}, DurationMs: sinceMs(began)}
		switch {
		case ctx.Err() != nil:
			resp.Cancelled, resp.Error = true, &QueryError{Message: msgCancelled}
		case runCtx.Err() != nil:
			resp.Cancelled, resp.Error = true, &QueryError{Message: msgTimedOut}
		default:
			resp.Error = &QueryError{Message: err.Error()}
		}
		return resp
	}
	var stmts []Statement
	if r.Preview {
		var qe *QueryError
		stmts, qe = PreviewStatements(r.Engine, r.SQL)
		if qe != nil {
			return QueryResponse{Results: []QueryResult{}, Preview: true, Error: qe, DurationMs: sinceMs(began)}
		}
	} else {
		var err error
		stmts, err = SplitMySQL(r.SQL)
		if err != nil {
			return failed(err)
		}
	}
	cfg := mysqlConfig(t, r.Database)
	db, conn, err := openConn(runCtx, cfg)
	if err != nil {
		return failed(err)
	}
	defer db.Close()
	defer conn.Close()
	for _, s := range mysqlSessionSQL(r.Engine, r.Writes || r.Preview) {
		if _, err := conn.ExecContext(runCtx, s); err != nil {
			return failed(err)
		}
	}
	if r.Preview {
		refusal, err := mysqlPreviewRefusal(runCtx, sqlConn{conn})
		if err != nil {
			return failed(err)
		}
		if refusal != nil {
			refusal.DurationMs = sinceMs(began)
			return *refusal
		}
	}
	var id uint64
	if err := conn.QueryRowContext(runCtx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		return failed(err)
	}

	var killOnce sync.Once
	killed := make(chan struct{})
	kill := func() {
		killOnce.Do(func() {
			defer close(killed)
			kctx, kcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer kcancel()
			kdb, kconn, err := openConn(kctx, mysqlConfig(t, ""))
			if err != nil {
				return
			}
			defer kdb.Close()
			defer kconn.Close()
			_, _ = kconn.ExecContext(kctx, "KILL QUERY "+strconv.FormatUint(id, 10))
		})
	}
	finished := make(chan struct{})
	var watch sync.WaitGroup
	watch.Add(1)
	go func() {
		defer watch.Done()
		select {
		case <-runCtx.Done():
			kill()
		case <-finished:
		}
	}()
	selfStop := false
	stop := func() {
		selfStop = true
		cancel() // the watcher sends the KILL
		kill()   // same Once: waits for the watcher's, or sends it first
		<-killed
	}

	run := runBatch
	if r.Preview {
		run = runPreviewBatch
	}
	resp, num := run(runCtx, sqlConn{conn}, stmts, stop)
	close(finished)
	watch.Wait()

	if resp.Stopped == "" && resp.Error != nil && !selfStop {
		switch {
		case ctx.Err() != nil:
			resp.Cancelled, resp.Error.Message = true, msgCancelled
		case runCtx.Err() != nil || num == mysqlErrQueryTimeout || num == mariadbErrQueryTimeout:
			resp.Cancelled, resp.Error.Message = true, msgTimedOut
		}
	}
	return resp
}

// SchemaMySQL lists the tables and views of database as one node.
func SchemaMySQL(ctx context.Context, t Target, database string) ([]SchemaNode, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, conn, err := openConn(ctx, mysqlConfig(t, database))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, "SELECT TABLE_NAME, TABLE_TYPE = 'VIEW' FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	node := SchemaNode{Name: database, Tables: []string{}, Views: []string{}}
	for rows.Next() {
		var name string
		var view bool
		if err := rows.Scan(&name, &view); err != nil {
			return nil, err
		}
		node.Tables = append(node.Tables, name)
		if view {
			node.Views = append(node.Views, name)
		}
	}
	return []SchemaNode{node}, rows.Err()
}
