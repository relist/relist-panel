# Databases phase 4 (Query panel) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin runs SQL against a Postgres, MySQL or MariaDB Database, or commands against a Redis Database, from its page. Runs are read-only by default and audited, with results in a table, so pgAdmin and phpMyAdmin are no longer needed.

**Architecture:**
- **A new package `internal/dbquery`** holds the engine runners: pgx for Postgres, go-sql-driver for MySQL and MariaDB, and a small RESP2 client for Redis. It also holds the pure parts: the MySQL splitter, the Redis argument splitter and command classifier, result shaping with caps, and password redaction.
- **Connections.** Each run opens its own connection over `relist-network` to `relist-db-<id>` and closes it afterwards, so session settings never leak between runs.
- **`internal/databases`** adds the routes. They check the Database is running, refuse under the Migration lock, and answer 200 for every run outcome, so the audit middleware records each run.
- **Delivery in two stacked PRs:**
  - **Part A (Tasks 1–6), SQL:** branch `claude/databases-phase-4`, on phase 3b.
  - **Part B (Tasks 7–9), Redis:** branch `claude/databases-phase-4b`, on Part A.

**Tech Stack:** Go, `github.com/jackc/pgx/v5` v5.12.0 (new), `github.com/go-sql-driver/mysql` v1.10.1 (new), React 19, antd 6.5.1, Monaco (already lazy-loaded), vitest and Playwright.

**Spec:** `.scratch/databases/spec.md`, sections "How tools reach the database" and "Query panel". The phase 4 decisions are already applied there, from probes run on pg 18, mysql 8.4, mariadb 11.8 and Redis 8 and 7.4 in the sandbox.

## Global Constraints

- **No migration.** Phase 4 stores nothing new; history lives in the browser.
- **Dependencies.**
  - Add exactly `github.com/jackc/pgx/v5@v5.12.0` and `github.com/go-sql-driver/mysql@v1.10.1`.
  - Use `pgx.Connect` / `pgconn`, never `pgxpool`.
  - Redis has no client library.
- **Limits:**

  | Limit | Value | Notes |
  |---|---|---|
  | `maxRows` | 1000 | per result |
  | `maxCell` | 65536 bytes | |
  | `maxResults` | 20 | per batch |
  | `maxResultBytes` | 16 MiB | per batch, the sum of the cells kept |
  | `maxBody` | 1 MiB | request body |
  | `runTimeout` | 30 s | the whole batch |
  | `auditText` | 4096 bytes | after redaction |
  | `redisMaxElems` | 1000 | per collection reply |
  | `redisMaxBulk` | 65536 bytes | per kept bulk string |

- **Per-run session settings:**
  - Postgres: runtime parameters `default_transaction_read_only=on` (when read-only), `statement_timeout=30000` and `application_name=relist-panel-query`.
  - MySQL: `SET SESSION transaction_read_only=ON` (when read-only) and `SET SESSION max_execution_time=30000`.
  - MariaDB: the same `transaction_read_only`, and `SET SESSION max_statement_time=30`.
- **Cancel:**
  - Postgres uses `pgconn.CancelRequestContextWatcherHandler` through `ConnConfig.BuildContextWatcherHandler`. pgx v5's default only sets a deadline, which would leave the query running on the server.
  - MySQL and MariaDB read `CONNECTION_ID()` when the run starts. When the context ends, the panel opens a second connection and sends `KILL QUERY <id>`.
- **Audit.**
  - `POST` run routes are in `authz.Actions`: `database.query` and `database.redis_command`. Every outcome (ok, server error, refused, stopped, cancelled) answers **200**, and the handler sets these `audit.Detail` keys: `database`, `statement` (redacted, cut to 4096), `writes`, `durationMs`, `rows`, `error`, `outcome`.
  - A malformed request, a stopped Database, or the Migration lock is 4xx and not a run.
- **Errors in responses** never carry the Database password.
- **Frontend.** Read `https://ant.design/llms.txt` and the antd pages you use (antd 6.5.1). Follow `docs/GUIDELINES.md`. Monaco gains the `pgsql` and `mysql` basic-language contributions in `web/src/monaco.ts`.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.

## Response shapes (shared by tasks)

```ts
type Cell = null | string | { text?: string; hex?: string; bytes: number }; // object = cut at maxCell or not UTF-8
interface QueryResult { statementIndex: number; columns: { name: string; type: string }[]; rows: Cell[][];
  truncated: boolean; command: string /* "SELECT 100", "UPDATE 3", "OK" */; affected: number | null; durationMs: number }
interface QueryResponse { results: QueryResult[]; durationMs: number;
  error: null | { message: string; statementIndex: number; statementStart: number /* char offset in the sent text */; position: number | null /* char offset in the sent text */ };
  stopped: null | string; cancelled: boolean }
```

- **The `stopped` texts** (exact):
  - "Stopped after 1,000 rows."
  - "Stopped after 20 results."
  - "Stopped: the results passed 16 MB."

  On Postgres with writes allowed, the sentence " Postgres runs a batch as one transaction, so its earlier statements were undone." is appended. On MySQL and MariaDB, " Statements after it did not run." is appended when any remain.
- **`cancelled`** is true when the request context ended or the 30 s timeout hit. `error.message` is then "Cancelled." or "Timed out after 30 s."

## Review Focus

1. **`SELECT * FROM big` on a 2M-row table.** 1,000 rows come back within 3 s, with `truncated` and `stopped`, and the query is no longer running on the server. *Test:* Task 6 e2e "row cap stops the server query", on every SQL engine.
2. **Cancel through Traefik.** After the browser aborts, `pg_stat_activity` / `PROCESSLIST` shows no running `pg_sleep(60)` / `SLEEP(60)` within 2 s, and an audit row with `outcome: cancelled` exists. *Test:* Task 6 e2e "cancel kills the server query and is audited".
3. **The read-only matrix on each engine.** The writes the spec lists are refused with the server's read-only error, and nothing changed. *Test:* Task 6 e2e "read-only refuses writes"; for Redis, Task 9 e2e "read-only refuses EVAL, FCALL, SET and CONFIG SET".
4. **A MySQL batch with a procedure body.** `DELIMITER //` … `CREATE PROCEDURE … BEGIN …; …; END//` … `DELIMITER ;` splits into one statement, and quotes or comments containing `;` never split. *Test:* Task 1 `TestSplitMySQL`.
5. **Big and odd values.** A `bigint` 9007199254740993 keeps every digit, a non-UTF-8 `bytea` or `BLOB` arrives as hex, and a 1 MB text cell is cut to 64 KB with its true size. *Tests:* Task 1 `TestShapeCell`, and Task 6 e2e "values keep their digits and bytes".

---

## File map

| File | Responsibility |
|---|---|
| `internal/dbquery/split.go` | `SplitMySQL` |
| `internal/dbquery/shape.go` | `Shape*` and `Budget`: cells, caps, stop texts |
| `internal/dbquery/redact.go` | `RedactSQL`, `RedactRedis` |
| `internal/dbquery/postgres.go` | `RunPostgres`, `SchemaPostgres` |
| `internal/dbquery/mysql.go` | `RunMySQL`, `SchemaMySQL` |
| `internal/dbquery/resp.go` (B) | RESP2 client with capped reads |
| `internal/dbquery/redis.go` (B) | `SplitRedisArgs`, `Classify`, `RunRedis`, `ScanKeys`, `ReadKey` |
| `internal/databases/query.go` | `Service.Query`, `Service.Schema`, `Service.Redis*`: target, gates |
| `internal/databases/query_http.go` | routes |
| `web/src/pages/DatabaseQueryTab.tsx` | SQL tab |
| `web/src/pages/DatabaseRedisTab.tsx` (B) | Redis console and key browser |
| `web/src/pages/query-view.ts` | CSV, history (localStorage), cell display, stop texts |

---

## Part A: SQL

### Task 1: Splitter, shaping and redaction

**Files:** Create `internal/dbquery/{split,shape,redact}.go` and their `_test.go`.

**Interfaces — Produces:**
```go
type Statement struct{ Text string; Start int } // Start: char (rune) offset of the first non-space char in the input
func SplitMySQL(sql string) ([]Statement, error) // error only for an unterminated quote or comment, naming its start line

type Cell = any // nil | string | CutCell
type CutCell struct{ Text string `json:"text,omitempty"`; Hex string `json:"hex,omitempty"`; Bytes int `json:"bytes"` }
func ShapeCell(raw []byte, isNull bool) Cell // nil → nil; valid UTF-8 ≤ maxCell → string; valid but longer → {Text: first maxCell bytes at a rune boundary, Bytes}; not UTF-8 → {Hex: hex of up to maxCell/2 bytes, Bytes}
type Budget struct{ /* rows in result, results, bytes */ }
func (b *Budget) Result() bool        // false once maxResults results were started
func (b *Budget) Row(cells []Cell) bool // false at row maxRows+1 of the result, or past maxResultBytes
func (b *Budget) Stopped() string     // the exact stop text, "" when not stopped
func RedactSQL(s string) string
func RedactRedis(args []string) []string
```

**`SplitMySQL` rules** (like the `mysql` client):
- The delimiter starts as `;`. A line whose first word is `DELIMITER` (any case) sets it to the next word, and the line itself is dropped.
- Outside quotes and comments, the delimiter ends a statement.
- `'…'` and `"…"` honour `\` escapes and doubled quotes.
- `` `…` `` honours doubled backticks.
- `-- ` (dash dash, then a space or end of line) and `#` run to the end of the line.
- `/* … */` is skipped, except `/*!…*/`, which stays in the text.
- Empty statements are dropped.

**`RedactSQL`:** case-insensitive. The first quoted literal after `PASSWORD`, `IDENTIFIED BY` or `IDENTIFIED WITH <word> BY` becomes `'***'`.

**`RedactRedis`:** every argument after `AUTH`. The value after `CONFIG SET requirepass` or `masterauth`. The arguments after `AUTH` / `AUTH2` inside `MIGRATE`. Any `ACL SETUSER` argument starting with `>` or `#`.

- [ ] **Step 1: Write the failing tests:**
  - `TestSplitMySQL`, a table test. Each input names the statements wanted.

    | Input | Want |
    |---|---|
    | `SELECT 1; SELECT 2` | 2 statements |
    | `SELECT ';'; SELECT "a;b"; SELECT 1 -- ;x` + newline + `;` | 3 statements |
    | `` SELECT `a;b` FROM t `` | 1 |
    | `/*! SET x=1 */; SELECT 1` | 2, and the first keeps `/*!` |
    | `# c;` + newline + `SELECT 1` | 1 |
    | `DELIMITER //` + newline + `CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END//` + newline + `DELIMITER ;` + newline + `CALL p();` | 2 |
    | `SELECT 'it\'s'; SELECT 2` | 2 |
    | `SELECT 'open` | error naming line 1 |

    Every `Start` points at the statement's first character in the input.
  - `TestShapeCell`:
    - `"9007199254740993"` stays that string;
    - the bytes `ff fe` → `{hex:"fffe", bytes:2}`;
    - a 70,000-byte UTF-8 string → `{text: 65,536 bytes or fewer, ending on a rune boundary, bytes:70000}`;
    - NULL → nil.
  - `TestBudget`:
    - row 1,001 → false, with stop text "Stopped after 1,000 rows.";
    - result 21 → false, "Stopped after 20 results.";
    - bytes past 16 MiB → false, "Stopped: the results passed 16 MB.".
  - `TestRedactSQL`:
    - `ALTER USER u PASSWORD 'x'` → `ALTER USER u PASSWORD '***'`;
    - `CREATE USER u IDENTIFIED BY "p;w"` → `… IDENTIFIED BY '***'`;
    - `IDENTIFIED WITH caching_sha2_password BY 'x'` → `… BY '***'`;
    - `SELECT 'password'` stays as it is.
  - `TestRedactRedis`:
    - `AUTH u p` → `AUTH *** ***`;
    - `CONFIG SET requirepass x` → `CONFIG SET requirepass ***`;
    - `ACL SETUSER u on >pw ~*` → `ACL SETUSER u on *** ~*`;
    - `GET k` stays as it is.
- [ ] **Step 2: Run** `go test ./internal/dbquery/`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat: split mysql batches, shape query cells and redact passwords`

### Task 2: Postgres runner

**Files:** Create `internal/dbquery/postgres.go` and `postgres_test.go`. Modify `go.mod`.

**Interfaces — Produces:**
```go
type Target struct{ Host string; Port int; User, Password string } // Host: relist-db-<id>
type Request struct{ Database, SQL string; Writes bool; Engine string }
func RunPostgres(ctx context.Context, t Target, r Request) QueryResponse
type SchemaNode struct{ Name string `json:"name"`; Tables []string `json:"tables"` }
func SchemaPostgres(ctx context.Context, t Target, database string) ([]SchemaNode, error) // schemas except pg_catalog, information_schema and pg_toast*, with tables and views sorted
```

**`RunPostgres`:**
- Build the config with `pgx.ParseConfig("")`, then set Host, Port, User, Password and Database, plus `RuntimeParams` and `BuildContextWatcherHandler` per Global Constraints. Bound the context with `runTimeout`, and connect.
- Call `conn.PgConn().Exec(ctx, r.SQL)`, which returns a `MultiResultReader`. For each `ResultReader`:
  - `Budget.Result()`;
  - columns from `FieldDescriptions()`, with type names from `conn.TypeMap().TypeForOID(oid)` (or `oid:<n>`);
  - rows through `Budget.Row`, with cells from `ShapeCell(value, value == nil)`.
- **On a budget stop:** cancel the context, close the connection, set `stopped`, mark the result `truncated`, and return the results so far.
- `command` comes from `CommandTag().String()`, and `affected` from `RowsAffected()` for INSERT, UPDATE, DELETE, MERGE and COPY.
- **On a server error** (`*pgconn.PgError`): `statementIndex` is the index of the failing result, and `position` is `Position − 1` (0-based, in characters). `statementStart` is that result's start; the simple protocol doesn't give it, so use 0.
- Always close the connection, with a 5 s context of its own.

- [ ] **Step 1: Write the failing tests.** Pure parts only:
  - `TestPgTypeName`: an unknown OID → `oid:<n>`;
  - `TestPgConfig`:
    - read-only sets `default_transaction_read_only=on`, `statement_timeout=30000` and `application_name`;
    - writes leave out the read-only setting;
    - the context-watcher handler is a `*pgconn.CancelRequestContextWatcherHandler`.

  The live behaviour is pinned by Task 6's e2e.
- [ ] **Step 2: Run** `go test ./internal/dbquery/ -run Pg`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat: run postgres queries for the query panel`

### Task 3: MySQL and MariaDB runner

**Files:** Create `internal/dbquery/mysql.go` and `mysql_test.go`. Modify `go.mod`.

**Interfaces — Produces:**
```go
func RunMySQL(ctx context.Context, t Target, r Request) QueryResponse // r.Engine "mysql" | "mariadb"
func SchemaMySQL(ctx context.Context, t Target, database string) ([]SchemaNode, error) // one node, Name = database, tables from information_schema.TABLES
func mysqlSessionSQL(engine string, writes bool) []string // the SET statements, in order
```

**`RunMySQL`:**
- Build the config with `mysql.NewConfig()`: User, Passwd, `Net="tcp"`, `Addr=host:port`, `DBName=r.Database`, `Timeout=5s`. `MultiStatements` stays false. Connect with `sql.OpenDB(connector).Conn(ctx)`, a single `*sql.Conn`.
- Run `mysqlSessionSQL`, then read `SELECT CONNECTION_ID()`.
- A watcher goroutine runs on `ctx.Done()`, unless the run already finished. It opens a second connection with `context.Background()` and a 5 s timeout, sends `KILL QUERY <id>`, and closes.
- `SplitMySQL(r.SQL)`. For each statement, call `conn.QueryContext`:
  - **with columns:** shape the rows through the Budget. Column types come from `DatabaseTypeName()`. Read cells as `sql.RawBytes`; nil is NULL.
  - **without columns:** run `SELECT ROW_COUNT()` on the same connection for `affected`, and set `command` to "OK".
- **On a budget stop:** cancel, which fires the KILL; close; and set `stopped`, with the remaining-statements suffix.
- **On an error** (`*mysql.MySQLError`): `statementIndex` is i and `statementStart` is `stmts[i].Start`. `position` stays nil.

- [ ] **Step 1: Write the failing tests:**
  - `TestMySQLSessionSQL`:
    - mysql, read-only → `["SET SESSION transaction_read_only=ON", "SET SESSION max_execution_time=30000"]`;
    - mariadb, writes → `["SET SESSION max_statement_time=30"]`.
  - `TestMySQLErrorPosition`: a batch whose second statement fails reports `statementIndex 1` and its `Start`. Use a fake that implements the minimal interface `RunMySQL` uses internally, `queryer`, through an unexported constructor.
- [ ] **Step 2: Run** `go test ./internal/dbquery/ -run MySQL`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat: run mysql and mariadb queries for the query panel`

### Task 4: Service and routes

**Files:** Create `internal/databases/query.go`, `query_http.go` and their tests. Modify `authz/actions.go`, `routescan_test.go` and `ports.go` (a `Querier` dependency, so tests can fake the runners).

**Interfaces — Produces:**
```go
type Querier interface {
	Run(ctx context.Context, t dbquery.Target, r dbquery.Request) dbquery.QueryResponse
	Schema(ctx context.Context, t dbquery.Target, engine, database string) ([]dbquery.SchemaNode, error)
}
func (s *Service) Query(ctx context.Context, id int64, r dbquery.Request) (dbquery.QueryResponse, error) // errors: ErrNotFound, ErrNotRunning, ErrLocked, ErrNoQuery (Redis on the SQL route)
func (s *Service) Schema(ctx context.Context, id int64, database string) ([]dbquery.SchemaNode, error)
```

**Routes:**

| Route | Body → reply | Action |
|---|---|---|
| `POST /api/databases/{id}/query` | `{database, sql, writes}` → 200 `QueryResponse` | `database.query` |
| `GET /api/databases/{id}/query/schema?database=` | → `[]SchemaNode` | — |

- **Errors:**
  - a body over 1 MiB → 413;
  - an empty `sql` → 400;
  - not running → 409;
  - under the lock → 409 `ErrLocked`;
  - a tombstone → 404.
- **The handler** sets the audit detail keys in Global Constraints. `outcome` is one of `ok`, `error`, `stopped` or `cancelled`. `rows` is the sum of the rows returned.
- **Target:** host `ContainerName(id)`, the engine's port, user `postgres` or `root`, and the stored password.

- [ ] **Step 1: Write the failing tests:**
  - `TestQueryRoute`, with a fake `Querier`:
    - read-only passes `Writes:false`;
    - the body is passed through;
    - a server error in the response still answers 200.
  - `TestQueryAuditsEveryOutcome`, using the authz middleware recorder as `internal/authz/record_test.go` does. One audit row per call, for ok, error, stopped and cancelled. A request whose context is cancelled before the fake returns still records a row with `outcome: cancelled`.
  - `TestQueryRedactsStatement`: `ALTER USER u PASSWORD 'x'` is recorded with `'***'`.
  - `TestQueryGates`: stopped → 409, lock → 409, Redis → 400, tombstone → 404, a 1 MiB + 1 body → 413.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run Query ./internal/authz/`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: serve the query panel api with an audit row per run`

### Task 5: Query tab

**Files:**
- Create `web/src/pages/DatabaseQueryTab.tsx`, `web/src/pages/query-view.ts` and their tests.
- Modify `web/src/monaco.ts` (add `basic-languages/pgsql/pgsql.contribution.js` and `mysql/mysql.contribution.js`), `DatabasePage.tsx` (a "Query" tab for SQL engines, after Connection), `data/api.ts` and `data/types.ts`.

**UI:**
- **Top bar:**
  - a database `Select`, fed by the logical databases list. The default is Postgres's `postgres`, or MySQL's first listed database.
  - an **Allow writes** `Switch`. It is not stored anywhere, and turning it on shows `Tag color="red"` "Writes allowed".
  - **Run** (Ctrl/Cmd+Enter) runs the selection, or else the whole editor.
  - **Cancel** shows while running, and aborts the fetch through an `AbortController`.
  - **History** opens a Drawer.
- **Left:** a `Tree` of schemas → tables. Clicking a table puts `SELECT * FROM "<schema>"."<table>" LIMIT 100` (with backticks for MySQL) in the editor and runs it.
- **Results:**
  - `Tabs`, one per result, labelled `1 · SELECT 100`.
  - Each has a `Table` with `virtual` and `scroll={{ y: 400 }}` (one with columns), or the text "<n> rows affected".
  - NULL shows as italic `NULL`. A cut cell shows its text with a "cut, <size>" tag. A hex cell shows `0x…` with a "binary" tag.
  - **Export CSV** builds the file from the shown rows in the browser.
  - `stopped` shows as an `Alert type="warning"`. The `error` shows as an `Alert type="error"`, and the editor marks the error position: `position` if set, else `statementStart`, plus the selection's start offset.
- **History:** the last 50 runs per Database, in `localStorage` under `relist.query.history.<id>`. Every read and write is wrapped in try/catch, and the page works without it.
- **Stopped Database:** the tab shows the reason, and Run is disabled.

- [ ] **Step 1: Write the failing tests.**
  - `query-view.test.ts`:
    - `toCSV` quotes `"`, commas and newlines, with NULL as empty;
    - `pushHistory` caps at 50 and survives a throwing `localStorage`;
    - `cellText` covers each Cell form.
  - `DatabaseQueryTab.test.tsx`, with Monaco mocked as in the other editor tests:
    - Run posts `{database:"postgres", sql, writes:false}`;
    - turning the switch on sends `writes:true`;
    - a result renders `1 · SELECT 1`;
    - `stopped` shows the warning;
    - an error shows its message;
    - Cancel aborts (the fetch signal's `aborted` is true);
    - clicking a table runs the `SELECT … LIMIT 100`.
- [ ] **Step 2: Run** `cd web && pnpm vitest run src/pages/DatabaseQueryTab src/pages/query-view`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `pnpm typecheck && pnpm vitest run --maxWorkers=6 && pnpm build`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: add the query tab for sql databases`

### Task 6: SQL e2e and live check

**Files:** Create `e2e/tests/databases-query.spec.ts`.

**The e2e spec** loops over postgres 18, mysql 8.4 and mariadb 11.8. It makes a fresh Database for each, seeds it through the API with writes allowed, and deletes it afterwards. All checks go through the panel URL (Traefik):
1. **"read-only refuses writes":**
   - each of `INSERT`, `CREATE TABLE`, `DROP TABLE`, `TRUNCATE`, `CREATE DATABASE` and `CREATE USER` (pg: `CREATE ROLE`) answers 200 with an `error` that holds the server's read-only text;
   - a later read shows nothing changed;
   - pg also refuses `SELECT nextval(...)` and `DO $$ … INSERT … $$`;
   - mysql and mariadb also refuse `CALL` of a writing procedure.
2. **"writes allowed"**: the same `INSERT` succeeds, with `affected: 1`.
3. **"row cap stops the server query"** on a 2M-row table:
   - `SELECT * FROM big` returns 1,000 rows within 3 s, with `truncated` and "Stopped after 1,000 rows.";
   - then the activity list (`pg_stat_activity` / `information_schema.PROCESSLIST`, read through the API) shows no `SELECT * FROM big`.
4. **"cancel kills the server query and is audited"**: start `SELECT pg_sleep(60)` / `SELECT SLEEP(60)` with `fetch` and abort it after 1 s. Within 2 s:
   - the activity list shows no such query;
   - `/api/audit` has a `database.query` row with `outcome: cancelled`.
5. **"values keep their digits and bytes":**
   - `SELECT 9007199254740993::bigint` (MySQL `SELECT 9007199254740993`) returns the exact string;
   - `SELECT '\xfffe'::bytea` on pg returns pg's text form `\xfffe`, a valid string; for the hex path, MySQL `SELECT UNHEX('FFFE')` returns `{hex:"fffe"}`;
   - `SELECT repeat('a', 1000000)` (MySQL `REPEAT`) returns `{text, bytes:1000000}`.
6. **"multi-statement":**
   - pg `SELECT 1; SELECT 2` gives two results;
   - pg `SELECT 1; SELEC 2` gives an error with a `position`;
   - mysql's DELIMITER procedure batch creates the procedure (with writes allowed), and `CALL p()` returns its rows.
7. **The UI path** on pg:
   1. open the Query tab;
   2. click a table in the tree;
   3. see the result tab;
   4. export the CSV (a download event).

- [ ] **Step 1: Write the spec. Step 2:** `task e2e:reload && task e2e -- databases-query`. Expected: PASS.
- [ ] **Step 3: Manual check** (`docs/agents/live-checks.md`) in a browser: run, cancel, Allow writes, history after reload, and the error marker on a multi-line selection.
- [ ] **Step 4: Run** the full suites:
  - `go test ./... && go vet ./...`;
  - `go test -race ./internal/dbquery/ ./internal/databases/`;
  - web typecheck, vitest (`--maxWorkers=6`) and build;
  - `task e2e`.

  Expected: all PASS.
- [ ] **Step 5: Commit** `test: cover the sql query panel end to end`

**Part A is complete here.** `git checkout -b claude/databases-phase-4b` for Part B.

---

## Part B: Redis

### Task 7: RESP2 client, argument splitter and classifier

**Files:** Create `internal/dbquery/resp.go`, `redis.go` and their tests.

**Interfaces — Produces:**
```go
// Reply is a RESP2 reply as JSON: {"type":"string"|"error"|"integer"|"bulk"|"nil"|"array", "value":…, "bytes":n?, "more":n?}
type Reply struct{ Type string `json:"type"`; Value any `json:"value"`; Bytes int `json:"bytes,omitempty"`; More int `json:"more,omitempty"` }
type respConn struct{ /* net.Conn + bufio */ }
func dialRedis(ctx context.Context, t Target, db int) (*respConn, error) // AUTH <password> (requirepass, user default), then SELECT db; deadline from ctx
func (c *respConn) Do(args ...string) (Reply, error)                     // capped read: bulk > redisMaxBulk is read to the end and discarded past the cap (Bytes = full size); arrays keep redisMaxElems and count the rest in More (the rest are still read off the wire)
func SplitRedisArgs(line string) ([]string, error)                       // redis-cli sdssplitargs rules
type Class struct{ Refused, Dangerous bool; Reason string }
func Classify(info CommandInfo, args []string, writes bool) Class
type CommandInfo struct{ Known bool; Flags []string; Subcommands map[string][]string } // from COMMAND INFO <name>
func ParseCommandInfo(r Reply) CommandInfo
```

**`SplitRedisArgs`:**
- Whitespace separates arguments.
- `"…"` takes `\n \r \t \b \a \\ \" \xHH`.
- `'…'` takes only `\'`.
- A closing quote must be followed by whitespace or the end, and an unbalanced quote is an error. These are `sdssplitargs`'s rules.

**`Classify`** checks, in order:
1. `args[0]` (upper-cased) is one of the never-in-console commands in the spec → Refused, "<CMD> is not available in the console" (plus "use the database selector" for SELECT).
2. `!info.Known` → Refused, "unknown command".
3. **Effective flags:** if the command has subcommands, use those of `name|args[1]`. A missing or unknown subcommand is Refused.
4. **Dangerous:** the spec's list, matched on the command, or on the command and subcommand.
5. **With writes off:** allowed if the effective flags contain `readonly`, or the command (or command and subcommand) is in the spec's read-only list. Otherwise Refused, "writes are off".

- [ ] **Step 1: Write the failing tests:**
  - `TestSplitRedisArgs`:
    - `set k "a b"` → `[set k "a b"]`, the third argument holding the space;
    - `"\x41\n"` → `"A\n"`;
    - `'it\'s'` → `it's`;
    - `"a"b` → error;
    - `"open` → error.
  - `TestClassify` (table). Write the flags exactly as probed (copy them from the spec's probe; for example `eval`: `noscript,stale,skip_monitor,no_mandatory_keys,movablekeys`):
    - `GET k` read-only → ok;
    - `SET k v` read-only → refused, "writes are off";
    - `EVAL "…" 0` read-only → refused;
    - `EVAL_RO` → ok;
    - `CONFIG GET maxmemory` → ok;
    - `CONFIG SET maxmemory 1` with writes on → dangerous;
    - `FLUSHALL` with writes on → dangerous;
    - `SUBSCRIBE c` with writes on → refused;
    - `SELECT 1` → refused, naming the selector;
    - `FOO` → refused, "unknown command";
    - `SCRIPT FLUSH` with writes on → dangerous;
    - `PING` read-only → ok.
  - `TestRespReadCaps`, against a `net.Pipe` fake server:
    - a 100,000-byte bulk → `{type:"bulk", value: 65,536 bytes, bytes:100000}`, and the next reply still parses;
    - a 1,500-element array → 1,000 kept, `more:500`, and the next reply parses;
    - an `-ERR` reply → `{type:"error"}`;
    - nested arrays work.
- [ ] **Step 2: Run** `go test ./internal/dbquery/ -run 'Redis|Classify|Resp'`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** `feat: speak resp2 and classify redis commands for the console`

### Task 8: Redis routes

**Files:** Modify `internal/dbquery/redis.go` (`RunRedis`, `ScanKeys`, `ReadKey`), `internal/databases/query.go`, `query_http.go`, `authz` and their tests.

**Interfaces:**
```go
func RunRedis(ctx context.Context, t Target, db int, line string, writes, confirmDangerous bool) RedisResponse
type RedisResponse struct{ Reply *Reply `json:"reply"`; Refused string `json:"refused,omitempty"`; NeedsConfirm bool `json:"needsConfirm,omitempty"`; Error string `json:"error,omitempty"`; DurationMs int64 `json:"durationMs"` }
func ScanKeys(ctx context.Context, t Target, db int, pattern, cursor string) (keys []KeyInfo, next string, err error) // SCAN cursor MATCH pattern COUNT 100, then TYPE and PTTL per key
type KeyInfo struct{ Key string `json:"key"`; Type string `json:"type"`; TTLms int64 `json:"ttlMs"` }
func ReadKey(ctx context.Context, t Target, db int, key string) (KeyValue, error) // string: GET; hash: HSCAN 0 COUNT 1000 once; list: LRANGE 0 999; set: SSCAN 0 COUNT 1000 once; zset: ZRANGE 0 999 WITHSCORES; other types: {type, unsupported:true}
```

**Routes:**

| Route | Body → reply | Action |
|---|---|---|
| `POST /api/databases/{id}/redis/command` | `{db, command, writes, confirmDangerous}` → 200 `RedisResponse` | `database.redis_command` |
| `GET /api/databases/{id}/redis/keys?db=&pattern=&cursor=` | → `{keys, cursor}` | — |
| `GET /api/databases/{id}/redis/key?db=&key=` | → `KeyValue` | — |

- `db` is 0–15; anything else is 400.
- A dangerous command without `confirmDangerous` answers 200 with `needsConfirm: true`, and **nothing is sent** to Redis.
- Refused commands answer 200 with `refused`, and are audited with `outcome: refused`.
- Audit detail: `database` = the db number, `statement` = `RedactRedis(args)` joined and cut, `writes`, `outcome`, `durationMs`, `error`.

- [ ] **Step 1: Write the failing tests**, with a fake runner:
  - `TestRedisRouteNeedsConfirm`: FLUSHALL with writes on and no confirm → `needsConfirm`. The runner was told not to send, and its sent-commands list is empty.
  - `TestRedisAuditsRefused`: `SET k v` with writes off → an audit row with `outcome: refused`.
  - `TestRedisRedactsAuth`: `CONFIG SET requirepass x` with writes and confirm is recorded as `CONFIG SET requirepass ***`.
  - `TestRedisGates`: db 16 → 400, stopped → 409, a SQL engine → 400.
- [ ] **Step 2: Run** `go test ./internal/databases/ -run Redis ./internal/authz/`. Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: serve the redis console and key browser api`

### Task 9: Redis tab, e2e and live check

**Files:** Create `web/src/pages/DatabaseRedisTab.tsx` and its test. Modify `DatabasePage.tsx` (a "Console" tab for Redis), `api.ts` and `types.ts`. Create `e2e/tests/databases-redis.spec.ts`.

**UI:**
- **Top bar:** a db `Select` (0–15) and the Allow writes `Switch`, the same as SQL.
- **Console:** an `Input` with history (Up/Down, the last 50, in `localStorage` `relist.redis.history.<id>`, guarded by try/catch), above an output log in monospace. Each entry shows the command and the reply, rendered `redis-cli`-style: `(integer) 1`, `"text"`, `(nil)`, numbered array lines, `(error) …`, and "… <n> more" / "cut, <size>" markers.
- **Confirmation:** `needsConfirm` opens a `Modal` with `okButtonProps danger`: "<CMD> can't be undone. Run it?". Confirming resends with `confirmDangerous: true`.
- **Key browser** beside the console:
  - a pattern `Input` (default `*`) and Scan, with "Load more" while the cursor is not `0`;
  - a `Table` of key, type `Tag` and TTL ("no expiry" for −1);
  - clicking a key shows its value by type, as a Descriptions or Table, with a "first 1,000" note when there are more.

- [ ] **Step 1: Write the failing tests.**
  - Vitest:
    - Enter posts `{db:0, command:"GET k", writes:false, confirmDangerous:false}`;
    - `needsConfirm` opens the modal, and OK resends with `confirmDangerous:true`;
    - `refused` shows in the log;
    - Up recalls the last command;
    - Load more sends the cursor;
    - clicking a key loads its value.
  - e2e `databases-redis.spec.ts` on redis 8 and 7.4, through the panel URL:
    - "read-only refuses EVAL, FCALL, SET and CONFIG SET", then `GET` works;
    - with writes on, `SET k v` then `GET k` returns `"v"`;
    - `FLUSHALL` without confirm → `needsConfirm`, and the key still exists;
    - with confirm, it is gone;
    - `SUBSCRIBE c` → refused;
    - a 100 KB value → `bytes:100000` and a cut value;
    - an audit row for each run, including refused ones, and `CONFIG SET requirepass` redacted;
    - the UI path: type a command, see the reply, scan keys, open a hash.
- [ ] **Step 2: Run** the vitest and `task e2e -- databases-redis`. Expected: FAIL, then PASS after Step 3. **Step 3: Implement.**
- [ ] **Step 4: Manual check** in a browser: the console, the dangerous-command confirmation, and the key browser on a hash, list, set and zset of 1,500 elements.
- [ ] **Step 5: Run** the full suites, as in Task 6 Step 4. **Commit** `feat: add the redis console and key browser`
