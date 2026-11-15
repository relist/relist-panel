package databases

import (
	"context"
	"errors"
	"strings"

	"relist-panel/internal/dbquery"
)

var ErrNoQuery = errors.New("this Database has no SQL console")

// Querier runs the Query panel's statements; tests fake it.
type Querier interface {
	Run(ctx context.Context, t dbquery.Target, r dbquery.Request) dbquery.QueryResponse
	Schema(ctx context.Context, t dbquery.Target, engine, database string) ([]dbquery.SchemaNode, error)
}

// engineQuerier is the real Querier: one driver per engine.
type engineQuerier struct{}

func (engineQuerier) Run(ctx context.Context, t dbquery.Target, r dbquery.Request) dbquery.QueryResponse {
	if r.Engine == "postgres" {
		return dbquery.RunPostgres(ctx, t, r)
	}
	return dbquery.RunMySQL(ctx, t, r)
}

func (engineQuerier) Schema(ctx context.Context, t dbquery.Target, engine, database string) ([]dbquery.SchemaNode, error) {
	if engine == "postgres" {
		return dbquery.SchemaPostgres(ctx, t, database)
	}
	return dbquery.SchemaMySQL(ctx, t, database)
}

// queryTarget checks the Database can take a run and says where it is. A
// tombstone is not found, the Migration lock refuses, and a Database that is
// not running has nothing to connect to.
func (s *Service) queryTarget(ctx context.Context, id int64, wantRedis bool) (Database, dbquery.Target, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return Database{}, dbquery.Target{}, err
	}
	if d.DeletedAt != nil {
		return Database{}, dbquery.Target{}, ErrNotFound
	}
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		return Database{}, dbquery.Target{}, err
	}
	if (d.Engine == "redis") != wantRedis {
		return Database{}, dbquery.Target{}, ErrNoQuery
	}
	if s.locked(ctx) {
		return Database{}, dbquery.Target{}, ErrLocked
	}
	st, err := s.d.Host.Inspect(ctx, ContainerName(id))
	if err != nil {
		return Database{}, dbquery.Target{}, err
	}
	if !st.Exists || !st.Running {
		return Database{}, dbquery.Target{}, ErrNotRunning
	}
	return d, dbquery.Target{Host: ContainerName(id), Port: eng.Port(), User: eng.User(), Password: d.Password}, nil
}

// Query runs r against Database id. Whatever the server answers, an error
// included, is in the response; the returned error is only for a run that
// could not start.
func (s *Service) Query(ctx context.Context, id int64, r dbquery.Request) (dbquery.QueryResponse, error) {
	d, t, err := s.queryTarget(ctx, id, false)
	if err != nil {
		return dbquery.QueryResponse{}, err
	}
	r.Engine = d.Engine
	resp := s.d.Querier.Run(ctx, t, r)
	if resp.Error != nil {
		resp.Error.Message = scrub(resp.Error.Message, d.Password)
	}
	resp.Stopped = scrub(resp.Stopped, d.Password)
	return resp, nil
}

// Schema lists the schemas and tables of one logical database.
func (s *Service) Schema(ctx context.Context, id int64, database string) ([]dbquery.SchemaNode, error) {
	d, t, err := s.queryTarget(ctx, id, false)
	if err != nil {
		return nil, err
	}
	nodes, err := s.d.Querier.Schema(ctx, t, d.Engine, database)
	if err != nil {
		return nil, errors.New(scrub(err.Error(), d.Password))
	}
	return nodes, nil
}

// scrub removes the Database's password from text that goes to the browser
// or the audit log.
func scrub(s, password string) string {
	if password == "" {
		return s
	}
	return strings.ReplaceAll(s, password, "***")
}
