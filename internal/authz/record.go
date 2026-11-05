package authz

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// target names the scope readably, e.g. "my-saas / production / api", or
// answers the path when the request has no scope or a name cannot be read.
func (a *Authorizer) target(ctx context.Context, pattern string, scope Scope, path string) string {
	if scope.ProjectID == 0 {
		return a.hostTarget(ctx, pattern, path)
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

// hostTables are the host-wide things whose `/api/<kind>/{id}` routes an
// event names by the thing's name.
var hostTables = map[string]string{
	"ssh-keys":     "ssh_keys",
	"certificates": "certificates",
	"builders":     "builders",
}

// hostTarget names the SSH key, Certificate or Builder an `/api/<kind>/{id}`
// route acts on, or answers the path.
func (a *Authorizer) hostTarget(ctx context.Context, pattern, path string) string {
	if _, rest, found := strings.Cut(pattern, " "); found {
		pattern = rest
	}
	patternSegs := strings.Split(strings.Trim(pattern, "/"), "/")
	pathSegs := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternSegs) < 3 || len(pathSegs) < 3 || patternSegs[0] != "api" || patternSegs[2] != "{id}" {
		return path
	}
	table, ok := hostTables[patternSegs[1]]
	if !ok {
		return path
	}
	id, err := strconv.ParseInt(pathSegs[2], 10, 64)
	if err != nil {
		return path
	}
	var name string
	if err := a.db.QueryRowContext(ctx, "SELECT name FROM "+table+" WHERE id = ?", id).Scan(&name); err != nil {
		return path
	}
	return name
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

// statusWriter remembers the status a handler answered with, and whether
// writing the body failed. A Write without WriteHeader is a 200.
type statusWriter struct {
	http.ResponseWriter
	status int
	failed bool
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
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		w.failed = true
	}
	return n, err
}

// ReadFrom keeps the underlying writer's ReadFrom (sendfile for a file)
// for io.Copy.
func (w *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	var n int64
	var err error
	if from, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = from.ReadFrom(src)
	} else {
		// Hide this ReadFrom from io.Copy, which would call it again.
		n, err = io.Copy(struct{ io.Writer }{w.ResponseWriter}, src)
	}
	if err != nil {
		w.failed = true
	}
	return n, err
}

func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// deniedPerMinute caps the denied events one User records per action a
// minute.
const deniedPerMinute = 10

// maxRangeDetail is how much of a Range header a partial download records.
const maxRangeDetail = 64
