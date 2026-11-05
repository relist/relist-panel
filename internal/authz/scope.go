package authz

import (
	"net/url"
	"strconv"
	"strings"
)

// Scope is the Project, Environment and Compose a request's path names; zero
// where the pattern names none.
type Scope struct {
	ProjectID, EnvironmentID, ComposeID int64
}

// scopeOf aligns path with the ServeMux pattern it matched and reads the
// ids. path is the escaped path (r.URL.EscapedPath), split on literal
// slashes as the mux does, each segment then unescaped. It never calls r.PathValue: the mux fills path values only inside
// its own ServeHTTP, after this middleware. `{projectID}`, or `{id}` right
// after `projects`, is the Project; `{environmentID}`, or `{id}` right after
// `environments`, the Environment; `{composeID}` the Compose. Any other
// wildcard is not a scope. ok is false when a scope segment is not a
// positive integer.
func scopeOf(pattern, path string) (Scope, bool) {
	if _, rest, found := strings.Cut(pattern, " "); found {
		pattern = rest
	}
	patternSegs := strings.Split(strings.Trim(pattern, "/"), "/")
	pathSegs := strings.Split(strings.Trim(path, "/"), "/")
	var scope Scope
	for i, seg := range patternSegs {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := seg[1 : len(seg)-1]
		prev := ""
		if i > 0 {
			prev = patternSegs[i-1]
		}
		var target *int64
		switch {
		case name == "projectID" || (name == "id" && prev == "projects"):
			target = &scope.ProjectID
		case name == "environmentID" || (name == "id" && prev == "environments"):
			target = &scope.EnvironmentID
		case name == "composeID":
			target = &scope.ComposeID
		default:
			continue
		}
		if i >= len(pathSegs) {
			return Scope{}, false
		}
		value, err := url.PathUnescape(pathSegs[i])
		if err != nil {
			return Scope{}, false
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return Scope{}, false
		}
		*target = id
	}
	return scope, true
}
