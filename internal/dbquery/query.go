package dbquery

import (
	"strings"
	"time"
)

// runTimeout bounds a whole batch.
const runTimeout = 30 * time.Second

// Target is where a run connects: Host is relist-db-<id> on relist-network.
type Target struct {
	Host           string
	Port           int
	User, Password string
}

// Request is one run. Engine is "postgres", "mysql" or "mariadb".
// Restricted runs the server-wide check on the run's own session before the
// first statement: a non-Admin's user must have no powers beyond its
// databases (serverwide.go).
type Request struct {
	Database, SQL string
	Writes        bool
	Engine        string
	Restricted    bool
	// Preview runs the batch inside a transaction that is rolled back; only
	// plain DML is allowed (PreviewStatements). It implies Writes.
	Preview bool
	// AsSuperuser is an Admin's choice to run as the superuser inside a
	// database another role owns; the engines never read it.
	AsSuperuser bool
}

// Column is a result column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryResult is one statement's result.
type QueryResult struct {
	StatementIndex int      `json:"statementIndex"`
	Columns        []Column `json:"columns"`
	Rows           [][]Cell `json:"rows"`
	Truncated      bool     `json:"truncated"`
	Command        string   `json:"command"`
	Affected       *int64   `json:"affected"`
	DurationMs     int64    `json:"durationMs"`
	// Verb and Target are set on Preview results: "delete" from "orders".
	Verb   string `json:"verb,omitempty"`
	Target string `json:"target,omitempty"`
}

// QueryError is the server's error, placed in the text that was sent.
type QueryError struct {
	Message        string `json:"message"`
	StatementIndex int    `json:"statementIndex"`
	StatementStart int    `json:"statementStart"`
	Position       *int   `json:"position"`
}

// QueryResponse is a run's outcome. Every outcome is data, never an HTTP
// error, so the audit middleware records it.
type QueryResponse struct {
	Results    []QueryResult `json:"results"`
	DurationMs int64         `json:"durationMs"`
	Error      *QueryError   `json:"error"`
	Stopped    string        `json:"stopped"`
	Cancelled  bool          `json:"cancelled"`
	// Preview: the run was a Preview, and nothing was kept.
	Preview bool `json:"preview,omitempty"`
	// User is the database user the run connected as.
	User string `json:"user"`
}

// SchemaNode is a schema and its tables and views. Views names the entries
// of Tables that are views (or materialized views): reading one runs the
// functions it calls with the reader's rights.
type SchemaNode struct {
	Name   string   `json:"name"`
	Tables []string `json:"tables"`
	Views  []string `json:"views"`
}

const (
	msgCancelled = "Cancelled."
	msgTimedOut  = "Timed out after 30 s."
)

func sinceMs(t time.Time) int64 { return time.Since(t).Milliseconds() }

func countAffected(tag string) *int64 {
	f := strings.Fields(tag)
	if len(f) == 0 {
		return nil
	}
	switch f[0] {
	case "INSERT", "UPDATE", "DELETE", "MERGE", "COPY":
		var n int64
		for _, c := range f[len(f)-1] {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int64(c-'0')
		}
		return &n
	}
	return nil
}
