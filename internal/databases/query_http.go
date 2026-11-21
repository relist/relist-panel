package databases

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"relist-panel/internal/audit"
	"relist-panel/internal/dbquery"
)

const (
	maxQueryBody = 1 << 20
	auditText    = 4096
)

// decodeBig reads a body of at most max bytes; one over it is 413.
func decodeBig(w http.ResponseWriter, r *http.Request, max int64, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, max)).Decode(out); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "the request is too large")
		} else {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
}

// cutText shortens s to at most n bytes at a character boundary.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func outcomeOf(resp dbquery.QueryResponse) string {
	switch {
	case resp.Preview:
		return "preview"
	case resp.Cancelled:
		return "cancelled"
	case resp.Error != nil:
		return "error"
	case resp.Stopped != "":
		return "stopped"
	}
	return "ok"
}

// query runs SQL. Every outcome of a run answers 200 (ok, error, stopped,
// cancelled): the audit middleware records only successes, and a run
// is to be recorded whatever it came to, even when the browser has gone.
func (a *API) query(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Database string `json:"database"`
		SQL      string `json:"sql"`
		Writes   bool   `json:"writes"`
		Preview  bool   `json:"preview"`
		// AsSuperuser: an Admin runs as the superuser even inside a
		// database another role owns. A grantee's is ignored.
		AsSuperuser bool `json:"asSuperuser"`
	}
	if !decodeBig(w, r, maxQueryBody, &in) {
		return
	}
	if strings.TrimSpace(in.SQL) == "" {
		writeJSONError(w, http.StatusBadRequest, "there is nothing to run")
		return
	}
	c, ok := callerOf(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	dbUser, err := a.sqlCaller(r, n, c)
	if err != nil {
		writeError(w, err)
		return
	}
	a.target(r, n)
	resp, err := a.s.Query(r.Context(), n, dbUser, dbquery.Request{Database: in.Database, SQL: in.SQL, Writes: in.Writes, Preview: in.Preview, AsSuperuser: in.AsSuperuser && c.Admin})
	if err != nil {
		writeError(w, err)
		return
	}
	rows := 0
	for _, res := range resp.Results {
		rows += len(res.Rows)
	}
	ctx := r.Context()
	audit.Detail(ctx, "database", in.Database)
	audit.Detail(ctx, "dbUser", resp.User)
	audit.Detail(ctx, "asSuperuser", in.AsSuperuser && c.Admin)
	audit.Detail(ctx, "statement", cutText(dbquery.RedactSQL(in.SQL), auditText))
	audit.Detail(ctx, "writes", in.Writes)
	audit.Detail(ctx, "durationMs", resp.DurationMs)
	audit.Detail(ctx, "rows", rows)
	if resp.Error != nil {
		audit.Detail(ctx, "error", cutText(resp.Error.Message, auditText))
	} else {
		audit.Detail(ctx, "error", "")
	}
	audit.Detail(ctx, "outcome", outcomeOf(resp))
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) querySchema(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	c, ok := callerOf(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	dbUser := ""
	if !c.Admin {
		if c.Grant.DBUser == "" {
			writeError(w, ErrNoQuery)
			return
		}
		dbUser = c.Grant.DBUser
	}
	nodes, err := a.s.Schema(r.Context(), n, dbUser, r.URL.Query().Get("database"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

// sqlCaller is which database user a SQL run connects as: the grant's for a
// grantee ("" is the superuser, an Admin's). The dbUser audit detail names
// it, with the engine's superuser spelled out.
func (a *API) sqlCaller(r *http.Request, n int64, c caller) (string, error) {
	if !c.Admin {
		if c.Grant.DBUser == "" {
			return "", ErrNoQuery
		}
		return c.Grant.DBUser, nil
	}
	return "", nil
}

// redisDB reads the logical database number: 0 to 15.
func redisDB(w http.ResponseWriter, raw string) (int, bool) {
	n, err := strconv.Atoi(raw)
	if raw == "" {
		n, err = 0, nil
	}
	if err != nil || n < 0 || n > 15 {
		writeJSONError(w, http.StatusBadRequest, "db must be a number from 0 to 15")
		return 0, false
	}
	return n, true
}

// auditStatement is a console line as the audit log records it: split like
// the console splits it, passwords replaced, cut. A line that cannot be split
// is not recorded: it might carry a password the redaction cannot find.
func auditStatement(line string) string {
	args, err := dbquery.SplitRedisArgs(line)
	if err != nil {
		return "(a line with unbalanced quotes)"
	}
	return cutText(strings.Join(dbquery.RedactRedis(args), " "), auditText)
}

// redisCommand runs one console line. Like a SQL run, every outcome answers
// 200 (ok, error, refused, cancelled) so the audit middleware records it.
func (a *API) redisCommand(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		DB               int    `json:"db"`
		Command          string `json:"command"`
		Writes           bool   `json:"writes"`
		ConfirmDangerous bool   `json:"confirmDangerous"`
	}
	if !decodeBig(w, r, maxQueryBody, &in) {
		return
	}
	if _, ok := redisDB(w, strconv.Itoa(in.DB)); !ok {
		return
	}
	if strings.TrimSpace(in.Command) == "" {
		writeJSONError(w, http.StatusBadRequest, "there is nothing to run")
		return
	}
	c, ok := callerOf(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	level := RedisLevel("")
	if !c.Admin {
		if c.Grant.Level == "" {
			writeError(w, ErrNoConsole)
			return
		}
		level = RedisLevel(c.Grant.Level)
	}
	a.target(r, n)
	resp, err := a.s.RedisCommand(r.Context(), n, level, in.DB, in.Command, in.Writes, in.ConfirmDangerous)
	if err != nil {
		writeError(w, err)
		return
	}
	outcome, failure := "ok", resp.Error
	switch {
	case resp.Cancelled:
		outcome = "cancelled"
	case resp.Error != "":
		outcome = "error"
	case resp.Refused != "":
		outcome, failure = "refused", resp.Refused
	case resp.NeedsConfirm:
		outcome, failure = "refused", "needs confirmation"
	case resp.Reply != nil && resp.Reply.Type == "error":
		outcome = "error"
		failure, _ = resp.Reply.Value.(string)
	}
	ctx := r.Context()
	audit.Detail(ctx, "database", in.DB)
	if level != "" {
		audit.Detail(ctx, "level", string(level))
	}
	audit.Detail(ctx, "statement", auditStatement(in.Command))
	audit.Detail(ctx, "writes", in.Writes)
	audit.Detail(ctx, "durationMs", resp.DurationMs)
	audit.Detail(ctx, "error", cutText(failure, auditText))
	audit.Detail(ctx, "outcome", outcome)
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) redisKeys(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	if _, ok := callerOf(r); !ok {
		writeError(w, ErrNotFound)
		return
	}
	q := r.URL.Query()
	db, ok := redisDB(w, q.Get("db"))
	if !ok {
		return
	}
	keys, cursor, err := a.s.RedisKeys(r.Context(), n, db, q.Get("pattern"), q.Get("cursor"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "cursor": cursor})
}

func (a *API) redisKey(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	if _, ok := callerOf(r); !ok {
		writeError(w, ErrNotFound)
		return
	}
	q := r.URL.Query()
	db, ok := redisDB(w, q.Get("db"))
	if !ok {
		return
	}
	if q.Get("key") == "" {
		writeJSONError(w, http.StatusBadRequest, "key is required")
		return
	}
	kv, err := a.s.RedisKey(r.Context(), n, db, q.Get("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kv)
}
