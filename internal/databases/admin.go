package databases

import (
	"context"

	"relist-panel/internal/dbquery"
)

// Admin runs the statements phase 7 sends to a Database as its superuser,
// over the network the way the Query panel does (ADR-0044). Tests fake it.
type Admin interface {
	// Exec runs each statement on its own, outside any transaction, and
	// stops at the first error. database "" is the engine's default.
	Exec(ctx context.Context, t dbquery.Target, engine, database string, stmts []string) error
	Rows(ctx context.Context, t dbquery.Target, engine, database, query string) ([][]string, error)
	// Ping logs in with t's password; it is how a password is proved.
	Ping(ctx context.Context, t dbquery.Target, engine string) error
	SetRedisPassword(ctx context.Context, t dbquery.Target, password string) error
}

// engineAdmin is the real Admin.
type engineAdmin struct{}

func (engineAdmin) Exec(ctx context.Context, t dbquery.Target, engine, database string, stmts []string) error {
	return dbquery.AdminExec(ctx, t, engine, database, stmts)
}

func (engineAdmin) Rows(ctx context.Context, t dbquery.Target, engine, database, query string) ([][]string, error) {
	return dbquery.AdminRows(ctx, t, engine, database, query)
}

func (engineAdmin) Ping(ctx context.Context, t dbquery.Target, engine string) error {
	return dbquery.Ping(ctx, t, engine)
}

func (engineAdmin) SetRedisPassword(ctx context.Context, t dbquery.Target, password string) error {
	return dbquery.SetRedisPassword(ctx, t, password)
}
