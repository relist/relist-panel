package databases

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"relist-panel/internal/audit"
	"relist-panel/internal/dbquery"
)

const maxQueryBody = 1 << 20

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
	}
	if !decodeBig(w, r, maxQueryBody, &in) {
		return
	}
	if strings.TrimSpace(in.SQL) == "" {
		writeJSONError(w, http.StatusBadRequest, "there is nothing to run")
		return
	}
	d := a.target(r, n)
	resp, err := a.s.Query(r.Context(), n, dbquery.Request{Database: in.Database, SQL: in.SQL, Writes: in.Writes, Preview: in.Preview})
	if err != nil {
		writeError(w, err)
		return
	}
	// What ran, never the text: no pattern finds every way to write a
	// password literal, and the server's message can quote the statement.
	rows, affected := 0, int64(0)
	for _, res := range resp.Results {
		rows += len(res.Rows)
		if res.Affected != nil {
			affected += *res.Affected
		}
	}
	code, failed := "", 0
	if resp.Error != nil {
		code, failed = resp.Error.Code, resp.Error.StatementIndex+1
	}
	ctx := r.Context()
	audit.Detail(ctx, "database", in.Database)
	audit.Detail(ctx, "statements", dbquery.StatementKinds(d.Engine, in.SQL))
	audit.Detail(ctx, "writes", in.Writes)
	audit.Detail(ctx, "durationMs", resp.DurationMs)
	audit.Detail(ctx, "rows", rows)
	audit.Detail(ctx, "affected", affected)
	audit.Detail(ctx, "errorCode", code)
	audit.Detail(ctx, "errorStatement", failed)
	audit.Detail(ctx, "outcome", outcomeOf(resp))
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) querySchema(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	nodes, err := a.s.Schema(r.Context(), n, r.URL.Query().Get("database"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
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
	a.target(r, n)
	resp, err := a.s.RedisCommand(r.Context(), n, in.DB, in.Command, in.Writes, in.ConfirmDangerous)
	if err != nil {
		writeError(w, err)
		return
	}
	// The command's name and the error's code, never an argument: Redis
	// quotes the arguments of an unknown command in its error.
	outcome, code := "ok", ""
	switch {
	case resp.Cancelled:
		outcome = "cancelled"
	case resp.Error != "":
		outcome = "error"
	case resp.Refused != "", resp.NeedsConfirm:
		outcome = "refused"
	case resp.Reply != nil && resp.Reply.Type == "error":
		outcome = "error"
		msg, _ := resp.Reply.Value.(string)
		code = redisErrorCode(msg)
	}
	command := resp.Command
	if command == "" {
		command = "OTHER"
	}
	ctx := r.Context()
	audit.Detail(ctx, "database", in.DB)
	audit.Detail(ctx, "command", command)
	audit.Detail(ctx, "writes", in.Writes)
	audit.Detail(ctx, "durationMs", resp.DurationMs)
	audit.Detail(ctx, "errorCode", code)
	audit.Detail(ctx, "outcome", outcome)
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) redisKeys(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
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

// redisErrorCode is an error reply's leading code (ERR, WRONGTYPE), or ""
// when it does not start with one.
func redisErrorCode(msg string) string {
	word, _, _ := strings.Cut(msg, " ")
	if word == "" || len(word) > 32 {
		return ""
	}
	for _, c := range word {
		if c < 'A' || c > 'Z' {
			return ""
		}
	}
	return word
}
