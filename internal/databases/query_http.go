package databases

import (
	"encoding/json"
	"errors"
	"net/http"
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
	}
	if !decodeBig(w, r, maxQueryBody, &in) {
		return
	}
	if strings.TrimSpace(in.SQL) == "" {
		writeJSONError(w, http.StatusBadRequest, "there is nothing to run")
		return
	}
	a.target(r, n)
	resp, err := a.s.Query(r.Context(), n, dbquery.Request{Database: in.Database, SQL: in.SQL, Writes: in.Writes})
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
	nodes, err := a.s.Schema(r.Context(), n, r.URL.Query().Get("database"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}
