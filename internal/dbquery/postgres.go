package dbquery

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgtype"
)

const pgStatementTimeoutMs = 30000

func newPgTypeMap() *pgtype.Map { return pgtype.NewMap() }

func pgTypeName(m *pgtype.Map, oid uint32) string {
	if t, ok := m.TypeForOID(oid); ok {
		return t.Name
	}
	return "oid:" + strconv.FormatUint(uint64(oid), 10)
}

// pgConfig builds the connection: plain TCP (a Database speaks no TLS on
// relist-network, and a fallback would dial the default host), the session's
// settings as runtime parameters, and a cancel that reaches the server: pgx's
// default only sets a socket deadline and leaves the query running.
func pgConfig(t Target, r Request) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, err
	}
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = t.Host, uint16(t.Port), t.User, t.Password, r.Database
	cfg.TLSConfig, cfg.Fallbacks = nil, nil
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{
		"statement_timeout": strconv.Itoa(pgStatementTimeoutMs),
		"application_name":  "relist-panel-query",
	}
	if !r.Writes {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		// The socket deadline must outlive the cancel request, or the
		// request's own context ends with it before it is sent.
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: time.Second}
	}
	return cfg, nil
}

// RunPostgres runs r.SQL as one simple-protocol query: the server splits it
// and runs it as one transaction.
func RunPostgres(ctx context.Context, t Target, r Request) QueryResponse {
	began := time.Now()
	resp := QueryResponse{Results: []QueryResult{}}
	runCtx, stop := context.WithTimeout(ctx, runTimeout)
	defer stop()

	cfg, err := pgConfig(t, r)
	if err != nil {
		resp.Error = &QueryError{Message: err.Error()}
		resp.DurationMs = sinceMs(began)
		return resp
	}
	conn, err := pgx.ConnectConfig(runCtx, cfg)
	if err != nil {
		finishPg(&resp, runCtx, ctx, err, began)
		return resp
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()

	if r.Restricted {
		rows, err := conn.Query(runCtx, PgServerWideQuery("session_user"))
		if err != nil {
			finishPg(&resp, runCtx, ctx, err, began)
			return resp
		}
		wide := rows.Next()
		rows.Close()
		if err := rows.Err(); err != nil {
			finishPg(&resp, runCtx, ctx, err, began)
			return resp
		}
		if wide {
			resp.Error = &QueryError{Message: ServerWideMessage(t.User)}
			resp.DurationMs = sinceMs(began)
			return resp
		}
	}

	var budget Budget
	mrr := conn.PgConn().Exec(runCtx, r.SQL)
	types := conn.TypeMap()
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		idx := len(resp.Results)
		if !budget.Result() {
			pgAbort(conn)
			break
		}
		start := time.Now()
		res := QueryResult{StatementIndex: idx, Rows: [][]Cell{}, Columns: []Column{}}
		for _, fd := range rr.FieldDescriptions() {
			res.Columns = append(res.Columns, Column{Name: fd.Name, Type: pgTypeName(types, fd.DataTypeOID)})
		}
		over := false
		for rr.NextRow() {
			vals := rr.Values()
			row := make([]Cell, len(vals))
			for i, v := range vals {
				row[i] = ShapeCell(v, v == nil)
			}
			if !budget.Row(row) {
				over = true
				break
			}
			res.Rows = append(res.Rows, row)
		}
		if over {
			res.Truncated = true
			res.Command = fmt.Sprintf("SELECT %d", len(res.Rows))
			res.DurationMs = sinceMs(start)
			resp.Results = append(resp.Results, res)
			pgAbort(conn)
			break
		}
		tag, err := rr.Close()
		if err != nil {
			finishPg(&resp, runCtx, ctx, err, began)
			return resp
		}
		res.Command = tag.String()
		res.Affected = countAffected(res.Command)
		if len(res.Columns) == 0 && res.Affected == nil && res.Command == "" {
			res.Command = "OK"
		}
		res.DurationMs = sinceMs(start)
		resp.Results = append(resp.Results, res)
	}
	if s := budget.Stopped(); s != "" {
		resp.Stopped = s
		if r.Writes {
			resp.Stopped += " Postgres runs a batch as one transaction, so its earlier statements were undone."
		}
		resp.DurationMs = sinceMs(began)
		return resp
	}
	if err := mrr.Close(); err != nil {
		finishPg(&resp, runCtx, ctx, err, began)
		return resp
	}
	resp.DurationMs = sinceMs(began)
	return resp
}

// pgAbort cancels what the server is running for conn, so it stops sending
// rows nobody reads; Close then drops the connection.
func pgAbort(conn *pgx.Conn) {
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = conn.PgConn().CancelRequest(cctx)
}

// finishPg turns an error into the response: cancelled when the request's
// context ended or the batch passed its time, the server's error otherwise.
func finishPg(resp *QueryResponse, runCtx, parent context.Context, err error, began time.Time) {
	resp.DurationMs = sinceMs(began)
	var pgErr *pgconn.PgError
	asPg := errors.As(err, &pgErr)
	switch {
	case parent.Err() != nil:
		resp.Cancelled = true
		resp.Error = &QueryError{Message: msgCancelled, StatementIndex: len(resp.Results)}
	case runCtx.Err() != nil || (asPg && pgErr.Code == "57014" && strings.Contains(pgErr.Message, "statement timeout")):
		resp.Cancelled = true
		resp.Error = &QueryError{Message: msgTimedOut, StatementIndex: len(resp.Results)}
	case asPg:
		qe := &QueryError{Message: pgErr.Message, StatementIndex: len(resp.Results)}
		if pgErr.Position > 0 {
			p := int(pgErr.Position) - 1
			qe.Position = &p
		}
		resp.Error = qe
	default:
		resp.Error = &QueryError{Message: err.Error(), StatementIndex: len(resp.Results)}
	}
}

// SchemaPostgres lists the schemas of database with their tables and views.
func SchemaPostgres(ctx context.Context, t Target, database string) ([]SchemaNode, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := pgConfig(t, Request{Database: database})
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		cctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = conn.Close(cctx)
	}()
	rows, err := conn.Query(ctx, `SELECT n.nspname, c.relname
		FROM pg_namespace n LEFT JOIN pg_class c ON c.relnamespace = n.oid AND c.relkind IN ('r','v','m','p','f')
		WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SchemaNode{}
	for rows.Next() {
		var schema string
		var table *string
		if err := rows.Scan(&schema, &table); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != schema {
			out = append(out, SchemaNode{Name: schema, Tables: []string{}})
		}
		if table != nil {
			out[len(out)-1].Tables = append(out[len(out)-1].Tables, *table)
		}
	}
	return out, rows.Err()
}
