package dbquery

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgTypeName(t *testing.T) {
	m := newPgTypeMap()
	if got := pgTypeName(m, 20); got != "int8" {
		t.Errorf("20 = %q", got)
	}
	if got := pgTypeName(m, 4294967295); got != "oid:4294967295" {
		t.Errorf("unknown = %q", got)
	}
}

func TestPgConfig(t *testing.T) {
	tg := Target{Host: "relist-db-3", Port: 5432, User: "postgres", Password: "pw"}
	ro, err := pgConfig(tg, Request{Database: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if ro.Host != "relist-db-3" || ro.Port != 5432 || ro.User != "postgres" || ro.Password != "pw" || ro.Database != "shop" {
		t.Errorf("target = %+v", ro)
	}
	if ro.TLSConfig != nil || len(ro.Fallbacks) != 0 {
		t.Errorf("TLS %v fallbacks %v: a Database on relist-network speaks plain, and a fallback would dial the default host", ro.TLSConfig, ro.Fallbacks)
	}
	if ro.RuntimeParams["default_transaction_read_only"] != "on" || ro.RuntimeParams["statement_timeout"] != "30000" || ro.RuntimeParams["application_name"] != "relist-panel-query" {
		t.Errorf("read-only params = %v", ro.RuntimeParams)
	}
	rw, _ := pgConfig(tg, Request{Database: "shop", Writes: true})
	if _, ok := rw.RuntimeParams["default_transaction_read_only"]; ok || rw.RuntimeParams["statement_timeout"] != "30000" {
		t.Errorf("writes params = %v", rw.RuntimeParams)
	}
	h := ro.BuildContextWatcherHandler(&pgconn.PgConn{})
	c, ok := h.(*pgconn.CancelRequestContextWatcherHandler)
	if !ok {
		t.Fatalf("handler is %T, want *pgconn.CancelRequestContextWatcherHandler", h)
	}
	if c.DeadlineDelay <= 0 {
		t.Errorf("DeadlineDelay %v: zero would expire the cancel request with the socket", c.DeadlineDelay)
	}
}

func TestCountAffected(t *testing.T) {
	cases := map[string]int64{"INSERT 0 3": 3, "UPDATE 5": 5, "DELETE 0": 0, "COPY 7": 7}
	for tag, want := range cases {
		if got := countAffected(tag); got == nil || *got != want {
			t.Errorf("%s = %v", tag, got)
		}
	}
	if countAffected("SELECT 4") != nil || countAffected("CREATE TABLE") != nil {
		t.Error("SELECT and DDL have no affected count")
	}
}

func TestPgConfigCatalogSearchPath(t *testing.T) {
	tg := Target{Host: "relist-db-1", Port: 5432, User: "postgres", Password: "pw"}
	plain, _ := pgConfig(tg, Request{Database: "shop"})
	pinned, _ := pgConfig(tg, Request{Database: "shop", CatalogSearchPath: true})
	if _, ok := plain.RuntimeParams["search_path"]; ok {
		t.Fatalf("plain run pins search_path: %v", plain.RuntimeParams)
	}
	if pinned.RuntimeParams["search_path"] != "pg_catalog, pg_temp" {
		t.Fatalf("pinned search_path = %q", pinned.RuntimeParams["search_path"])
	}
}
