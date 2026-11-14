package databases

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrUnknownEngine  = errors.New("unknown database engine")
	ErrUnknownVersion = errors.New("unknown version for this engine")
)

type Version struct {
	Tag     string
	Default bool
}

// Engine is the driver for one kind of server: what image, env, data path,
// port and healthcheck it needs, and how a client connects (ADR-0039).
type Engine interface {
	Name() string                 // "postgres"
	Label() string                // "PostgreSQL"
	Versions() []Version          // the tags the panel offers
	Image(tag string) string      // "postgres:18"
	Port() int                    // 5432 / 3306 / 3306 / 6379
	DataPath(tag string) string   // mount point of the volume
	User() string                 // "postgres" / "root" / "root" / "default"
	DefaultDatabase() string      // "postgres" / "" / "" / "0"
	Env(password string) []string // "KEY=value"
	Cmd() []string                // nil = image default
	Healthcheck() []string        // Docker Test array
	URL(host, password, database string) string
}

// Engines are the drivers, keyed by Name().
var Engines = map[string]Engine{}

func register(e Engine) { Engines[e.Name()] = e }

func init() {
	register(spec{
		name: "postgres", label: "PostgreSQL", port: 5432, user: "postgres", defaultDB: "postgres", scheme: "postgres",
		versions:    []Version{{"18", true}, {"17", false}, {"16", false}},
		envKey:      "POSTGRES_PASSWORD",
		healthcheck: "pg_isready -U postgres -h 127.0.0.1",
		dataPath: func(tag string) string {
			if major, err := strconv.Atoi(strings.SplitN(tag, ".", 2)[0]); err == nil && major >= 18 {
				return "/var/lib/postgresql"
			}
			return "/var/lib/postgresql/data"
		},
	})
	register(spec{
		name: "mysql", label: "MySQL", port: 3306, user: "root", scheme: "mysql",
		versions:    []Version{{"8.4", true}},
		envKey:      "MYSQL_ROOT_PASSWORD",
		healthcheck: `mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD" --silent`,
		dataPath:    func(string) string { return "/var/lib/mysql" },
	})
	register(spec{
		name: "mariadb", label: "MariaDB", port: 3306, user: "root", scheme: "mysql",
		versions:    []Version{{"11.8", true}, {"11.4", false}},
		envKey:      "MARIADB_ROOT_PASSWORD",
		healthcheck: "healthcheck.sh --connect --innodb_initialized",
		dataPath:    func(string) string { return "/var/lib/mysql" },
	})
	register(spec{
		name: "redis", label: "Redis", port: 6379, user: "default", defaultDB: "0", scheme: "redis",
		versions:    []Version{{"8", true}, {"7.4", false}},
		envKey:      "REDIS_PASSWORD",
		healthcheck: `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning ping | grep -q PONG`,
		dataPath:    func(string) string { return "/data" },
		// The password stays in the environment so it is not in `docker inspect` Cmd or ps.
		cmd: []string{"sh", "-c", `exec redis-server --requirepass "$REDIS_PASSWORD"`},
	})
}

// spec is the one Engine implementation; the four drivers differ only in data.
type spec struct {
	name, label, user, defaultDB, scheme string
	port                                 int
	versions                             []Version
	envKey, healthcheck                  string
	dataPath                             func(tag string) string
	cmd                                  []string
}

func (s spec) Name() string                 { return s.name }
func (s spec) Label() string                { return s.label }
func (s spec) Versions() []Version          { return append([]Version(nil), s.versions...) }
func (s spec) Image(tag string) string      { return s.name + ":" + tag }
func (s spec) Port() int                    { return s.port }
func (s spec) DataPath(tag string) string   { return s.dataPath(tag) }
func (s spec) User() string                 { return s.user }
func (s spec) DefaultDatabase() string      { return s.defaultDB }
func (s spec) Env(password string) []string { return []string{s.envKey + "=" + password} }
func (s spec) Cmd() []string                { return append([]string(nil), s.cmd...) }
func (s spec) Healthcheck() []string        { return []string{"CMD-SHELL", s.healthcheck} }

func (s spec) URL(host, password, database string) string {
	u := url.URL{
		Scheme: s.scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(s.port)),
	}
	if s.name == "redis" {
		u.User = url.UserPassword("", password)
	} else {
		u.User = url.UserPassword(s.user, password)
	}
	if database != "" {
		u.Path = "/" + database
	}
	return u.String()
}

// Lookup returns the driver for engine after checking tag is one of its versions.
func Lookup(engine, tag string) (Engine, error) {
	e, ok := Engines[engine]
	if !ok {
		return nil, ErrUnknownEngine
	}
	for _, v := range e.Versions() {
		if v.Tag == tag {
			return e, nil
		}
	}
	return nil, ErrUnknownVersion
}
