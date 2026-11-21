package dbquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
)

// adminTimeout bounds one admin call; connecting is bounded by 5 s inside it.
const adminTimeout = 60 * time.Second

// adminPgConfig is pgConfig for the admin path. It sets no read-only mode and
// no query label, and it turns statement logging off for the session:
// without log_min_error_statement=panic a failed CREATE ROLE ... PASSWORD
// '...' is written to the container log, where the Logs tab shows it.
// database "" connects to postgres.
func adminPgConfig(t Target, database string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, err
	}
	if database == "" {
		database = "postgres"
	}
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = t.Host, uint16(t.Port), t.User, t.Password, database
	cfg.TLSConfig, cfg.Fallbacks = nil, nil
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{
		"application_name":        "relist-panel-admin",
		"log_min_error_statement": "panic",
		"log_statement":           "none",
	}
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: time.Second}
	}
	return cfg, nil
}

func pgAdminConn(ctx context.Context, t Target, database string) (*pgx.Conn, error) {
	cfg, err := adminPgConfig(t, database)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}

func closePg(conn *pgx.Conn) {
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = conn.Close(cctx)
}

// AdminExec runs each statement on its own, in order, outside any
// transaction, and stops at the first error. database "" is the engine's
// default (postgres on Postgres, none on MySQL). The error never holds a
// statement: statements carry passwords.
func AdminExec(ctx context.Context, t Target, engine, database string, stmts []string) error {
	ctx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	if engine == "postgres" {
		conn, err := pgAdminConn(ctx, t, database)
		if err != nil {
			return err
		}
		defer closePg(conn)
		for _, s := range stmts {
			// PgConn().Exec is the simple protocol, so CREATE DATABASE is
			// never inside a transaction.
			if _, err := conn.PgConn().Exec(ctx, s).ReadAll(); err != nil {
				return err
			}
		}
		return nil
	}
	db, conn, err := openConn(ctx, mysqlConfig(t, database))
	if err != nil {
		return err
	}
	defer db.Close()
	defer conn.Close()
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// AdminRows runs one query and returns its rows as text; NULL is "".
func AdminRows(ctx context.Context, t Target, engine, database, query string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	out := [][]string{}
	if engine == "postgres" {
		conn, err := pgAdminConn(ctx, t, database)
		if err != nil {
			return nil, err
		}
		defer closePg(conn)
		results, err := conn.PgConn().Exec(ctx, query).ReadAll()
		if err != nil {
			return nil, err
		}
		for _, res := range results {
			for _, row := range res.Rows {
				cells := make([]string, len(row))
				for i, v := range row {
					cells[i] = string(v) // nil (NULL) is ""
				}
				out = append(out, cells)
			}
		}
		return out, nil
	}
	db, conn, err := openConn(ctx, mysqlConfig(t, database))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		cells := make([]string, len(cols))
		for i, v := range vals {
			cells[i] = v.String
		}
		out = append(out, cells)
	}
	return out, rows.Err()
}

// Ping proves t's password works over the network: a login and SELECT 1, or
// AUTH for Redis. Inside the container the local socket is trusted, so
// `docker exec` proves nothing about a password.
func Ping(ctx context.Context, t Target, engine string) error {
	ctx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	switch engine {
	case "redis":
		c, err := dialRedis(ctx, t, 0)
		if err != nil {
			return err
		}
		c.close()
		return nil
	case "postgres":
		conn, err := pgAdminConn(ctx, t, "")
		if err != nil {
			return err
		}
		defer closePg(conn)
		_, err = conn.PgConn().Exec(ctx, "SELECT 1").ReadAll()
		return err
	}
	db, conn, err := openConn(ctx, mysqlConfig(t, ""))
	if err != nil {
		return err
	}
	defer db.Close()
	defer conn.Close()
	return conn.PingContext(ctx)
}

// SetRedisPassword runs CONFIG SET requirepass on the server t points at,
// logged in with t's (old) password.
func SetRedisPassword(ctx context.Context, t Target, password string) error {
	ctx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	c, err := dialRedis(ctx, t, 0)
	if err != nil {
		return err
	}
	defer c.close()
	r, err := c.Do("CONFIG", "SET", "requirepass", password)
	if err != nil {
		return err
	}
	if r.Type == "error" {
		return errors.New(fmt.Sprint(r.Value))
	}
	return nil
}
