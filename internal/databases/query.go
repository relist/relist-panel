package databases

import (
	"context"
	"errors"
	"strings"

	"relist-panel/internal/dbquery"
)

var (
	ErrNoQuery   = errors.New("this Database has no SQL console")
	ErrNoConsole = errors.New("this Database has no Redis console")
)

// Querier runs the Query panel's statements; tests fake it.
type Querier interface {
	Run(ctx context.Context, t dbquery.Target, r dbquery.Request) dbquery.QueryResponse
	Schema(ctx context.Context, t dbquery.Target, engine, database string) ([]dbquery.SchemaNode, error)
	// RunRedis never sends a refused command, nor a dangerous one before
	// confirmDangerous.
	RunRedis(ctx context.Context, t dbquery.Target, db int, line string, writes, confirmDangerous bool) dbquery.RedisResponse
	ScanKeys(ctx context.Context, t dbquery.Target, db int, pattern, cursor string) ([]dbquery.KeyInfo, string, error)
	ReadKey(ctx context.Context, t dbquery.Target, db int, key string) (dbquery.KeyValue, error)
}

// engineQuerier is the real Querier: one driver per engine.
type engineQuerier struct{}

func (engineQuerier) Run(ctx context.Context, t dbquery.Target, r dbquery.Request) dbquery.QueryResponse {
	if r.Engine == "postgres" {
		return dbquery.RunPostgres(ctx, t, r)
	}
	return dbquery.RunMySQL(ctx, t, r)
}

func (engineQuerier) RunRedis(ctx context.Context, t dbquery.Target, db int, line string, writes, confirm bool) dbquery.RedisResponse {
	return dbquery.RunRedis(ctx, t, db, line, writes, confirm)
}

func (engineQuerier) ScanKeys(ctx context.Context, t dbquery.Target, db int, pattern, cursor string) ([]dbquery.KeyInfo, string, error) {
	return dbquery.ScanKeys(ctx, t, db, pattern, cursor)
}

func (engineQuerier) ReadKey(ctx context.Context, t dbquery.Target, db int, key string) (dbquery.KeyValue, error) {
	return dbquery.ReadKey(ctx, t, db, key)
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
		if wantRedis {
			return Database{}, dbquery.Target{}, ErrNoConsole
		}
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
	if r.Preview {
		r.Writes = true
	}
	r.Engine = d.Engine
	resp := s.d.Querier.Run(ctx, t, r)
	if r.Preview {
		// A Preview whatever came of it: a run that failed before the engine
		// marked the response still answers and audits as one.
		resp.Preview = true
	}
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

// RedisCommand runs one console line on logical database db.
func (s *Service) RedisCommand(ctx context.Context, id int64, db int, line string, writes, confirmDangerous bool) (dbquery.RedisResponse, error) {
	d, t, err := s.queryTarget(ctx, id, true)
	if err != nil {
		return dbquery.RedisResponse{}, err
	}
	resp := s.d.Querier.RunRedis(ctx, t, db, line, writes, confirmDangerous)
	resp.Error = scrub(resp.Error, d.Password)
	return resp, nil
}

// RedisKeys reads one page of a key scan.
func (s *Service) RedisKeys(ctx context.Context, id int64, db int, pattern, cursor string) ([]dbquery.KeyInfo, string, error) {
	d, t, err := s.queryTarget(ctx, id, true)
	if err != nil {
		return nil, "", err
	}
	keys, next, err := s.d.Querier.ScanKeys(ctx, t, db, pattern, cursor)
	if err != nil {
		return nil, "", errors.New(scrub(err.Error(), d.Password))
	}
	return keys, next, nil
}

// RedisKey reads one key's value.
func (s *Service) RedisKey(ctx context.Context, id int64, db int, key string) (dbquery.KeyValue, error) {
	d, t, err := s.queryTarget(ctx, id, true)
	if err != nil {
		return dbquery.KeyValue{}, err
	}
	kv, err := s.d.Querier.ReadKey(ctx, t, db, key)
	if err != nil {
		return dbquery.KeyValue{}, errors.New(scrub(err.Error(), d.Password))
	}
	return kv, nil
}
