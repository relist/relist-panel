package authz

import (
	"bufio"
	"context"
	"database/sql"
	"net"
	"net/http"
	"strings"
)

// target names the scope readably, e.g. "my-saas / production / api", or
// answers the path when the request has no scope or a name cannot be read.
func (a *Authorizer) target(ctx context.Context, scope Scope, path string) string {
	if scope.ProjectID == 0 {
		return path
	}
	var project, environment, compose sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT
		(SELECT name FROM projects WHERE id = ?),
		(SELECT name FROM environments WHERE id = ?),
		(SELECT name FROM composes WHERE id = ?)`,
		scope.ProjectID, scope.EnvironmentID, scope.ComposeID).Scan(&project, &environment, &compose)
	if err != nil || !project.Valid {
		return path
	}
	parts := []string{project.String}
	for _, name := range []sql.NullString{environment, compose} {
		if name.Valid {
			parts = append(parts, name.String)
		}
	}
	return strings.Join(parts, " / ")
}

// peerIP is the TCP peer, as the Access log records it; never a forwarded
// header.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// statusWriter remembers the status a handler answered with. A Write
// without WriteHeader is a 200.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
