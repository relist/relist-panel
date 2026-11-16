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
	// RunRedisGrantee is a non-Admin's run: stricter, and never confirmable.
	RunRedisGrantee(ctx context.Context, t dbquery.Target, db int, line string, readWrite bool) dbquery.RedisResponse
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

func (engineQuerier) RunRedisGrantee(ctx context.Context, t dbquery.Target, db int, line string, readWrite bool) dbquery.RedisResponse {
	return dbquery.RunRedisGrantee(ctx, t, db, line, readWrite)
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

// granteeTarget swaps the superuser for managed user dbUser on t, and says
// which passwords a response must be scrubbed of. An empty dbUser is the
// superuser; it is never a fallback for a non-Admin, which the HTTP layer
// guarantees before calling.
func (s *Service) granteeTarget(ctx context.Context, d Database, t dbquery.Target, dbUser string) (dbquery.Target, []string, error) {
	passwords := []string{d.Password}
	if dbUser == "" {
		return t, passwords, nil
	}
	u, err := s.d.Store.GetUser(ctx, d.ID, dbUser)
	if err != nil {
		return dbquery.Target{}, nil, err
	}
	t.User, t.Password = dbUser, u.Password
	return t, append(passwords, u.Password), nil
}

// Query runs r against Database id, as the superuser (dbUser "") or as a
// managed user, restricted. Whatever the server answers, an error included,
// is in the response; the returned error is only for a run that could not
// start.
func (s *Service) Query(ctx context.Context, id int64, dbUser string, r dbquery.Request) (dbquery.QueryResponse, error) {
	d, t, err := s.queryTarget(ctx, id, false)
	if err != nil {
		return dbquery.QueryResponse{}, err
	}
	t, passwords, err := s.granteeTarget(ctx, d, t, dbUser)
	if err != nil {
		return dbquery.QueryResponse{}, err
	}
	r.Restricted = dbUser != ""
	if r.Preview {
		r.Writes = true
	}
	r.Engine = d.Engine
	resp := s.d.Querier.Run(ctx, t, r)
	if resp.Error != nil {
		resp.Error.Message = scrubAll(resp.Error.Message, passwords...)
	}
	resp.Stopped = scrubAll(resp.Stopped, passwords...)
	return resp, nil
}

// Schema lists the schemas and tables of one logical database, connected as
// dbUser when it is set.
func (s *Service) Schema(ctx context.Context, id int64, dbUser, database string) ([]dbquery.SchemaNode, error) {
	d, t, err := s.queryTarget(ctx, id, false)
	if err != nil {
		return nil, err
	}
	t, passwords, err := s.granteeTarget(ctx, d, t, dbUser)
	if err != nil {
		return nil, err
	}
	nodes, err := s.d.Querier.Schema(ctx, t, d.Engine, database)
	if err != nil {
		return nil, errors.New(scrubAll(err.Error(), passwords...))
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

// RedisCommand runs one console line on logical database db. level "" is an
// Admin; a grantee's level picks the stricter classifier, and its confirm is
// ignored: nothing it runs is confirmable.
func (s *Service) RedisCommand(ctx context.Context, id int64, level RedisLevel, db int, line string, writes, confirmDangerous bool) (dbquery.RedisResponse, error) {
	d, t, err := s.queryTarget(ctx, id, true)
	if err != nil {
		return dbquery.RedisResponse{}, err
	}
	var resp dbquery.RedisResponse
	switch level {
	case "":
		resp = s.d.Querier.RunRedis(ctx, t, db, line, writes, confirmDangerous)
	case LevelRead:
		resp = s.d.Querier.RunRedisGrantee(ctx, t, db, line, false)
	case LevelReadWrite:
		resp = s.d.Querier.RunRedisGrantee(ctx, t, db, line, writes)
	default:
		return dbquery.RedisResponse{}, ErrBadGrant{Reason: "unknown level"}
	}
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
