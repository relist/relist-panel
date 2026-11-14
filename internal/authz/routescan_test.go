package authz

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// adminOnly is every registered pattern deliberately left out of Routes:
// only an admin reaches it. A new route must be added here or to Routes.
var adminOnly = []string{
	"GET /api/audit",
	"GET /api/audit/settings",
	"PUT /api/audit/settings",
	// An approver approves; the handler narrows admins to the Owner when
	// RELIST_POLICY_APPROVER=owner.
	"POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/approval",
	// Only an admin writes a Compose's YAML: creating one, saving it, or
	// resolving an unsaved one (ADR-0032).
	"POST /api/projects/{projectID}/environments/{environmentID}/composes",
	"PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}",
	"POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/preview",
	"POST /api/projects",
	"DELETE /api/projects/{id}",
	"GET /api/projects/{id}/removal-plan",
	"GET /api/projects/{id}/grants",
	"PUT /api/projects/{id}/grants",
	"GET /api/users",
	"POST /api/users",
	"GET /api/users/{id}",
	"PATCH /api/users/{id}",
	"DELETE /api/users/{id}",
	"POST /api/users/{id}/reset",
	"GET /api/users/{id}/grants",
	"PUT /api/users/{id}/grants",
	"POST /api/builders",
	"GET /api/registry",
	"GET /api/databases",
	"GET /api/databases/engines",
	"POST /api/databases",
	"GET /api/databases/{id}",
	"PATCH /api/databases/{id}",
	"DELETE /api/databases/{id}",
	"GET /api/databases/{id}/credentials",
	"GET /api/databases/{id}/logs",
	"POST /api/databases/{id}/start",
	"POST /api/databases/{id}/stop",
	"POST /api/databases/{id}/restart",
	"POST /api/databases/{id}/repair",
	"POST /api/databases/{id}/update-image",
	"POST /api/databases/{id}/apply-settings",
	"GET /api/registry/cleanup",
	"PUT /api/registry/cleanup",
	"GET /api/registry/cleanup/preview",
	"POST /api/registry/cleanup/run",
	"PUT /api/registry",
	"POST /api/registry/enable",
	"POST /api/registry/disable",
	"POST /api/registry/repair",
	"POST /api/registry/check",
	"DELETE /api/registry/data",
	"GET /api/registry/credentials",
	"POST /api/registry/credentials",
	"DELETE /api/registry/credentials/{id}",
	"GET /api/registry/repositories",
	"GET /api/registry/tags",
	"DELETE /api/registry/tags",
	"GET /api/registry/external",
	"POST /api/registry/external",
	"PUT /api/registry/external/{id}",
	"DELETE /api/registry/external/{id}",
	"POST /api/registry/external/{id}/test",
	"PUT /api/builders/default",
	"PUT /api/builders/settings",
	"PUT /api/builders/{id}",
	"DELETE /api/builders/{id}",
	"GET /api/builders/{id}/disk-usage",
	"POST /api/builders/{id}/prune",
	"POST /api/builders/{id}/start",
	"GET /api/auth-groups",
	"POST /api/auth-groups",
	"GET /api/auth-groups/{id}",
	"PATCH /api/auth-groups/{id}",
	"DELETE /api/auth-groups/{id}",
	"POST /api/auth-groups/{id}/users",
	"PATCH /api/auth-groups/{id}/users/{uid}",
	"DELETE /api/auth-groups/{id}/users/{uid}",
	"GET /api/auth-groups/{id}/users/{uid}/password",
	"GET /api/certificates",
	"POST /api/certificates",
	"PUT /api/certificates/{id}",
	"DELETE /api/certificates/{id}",
	"GET /api/docker",
	"POST /api/docker/prune",
	"GET /api/docker/prune-plan",
	"POST /api/docker/recover",
	"GET /api/host/metrics",
	"GET /api/images",
	"PUT /api/images",
	"GET /api/requests/metrics",
	"GET /api/requests/panel/metrics",
	"GET /api/requests/settings",
	"PUT /api/requests/settings",
	"GET /api/panel-update/status",
	"POST /api/panel-update/check",
	"PUT /api/panel-update/settings",
	"POST /api/panel-update/preflight",
	"POST /api/panel-update/apply",
	"POST /api/panel-update/cancel",
	"POST /api/ssh-keys",
	"PATCH /api/ssh-keys/{id}",
	"DELETE /api/ssh-keys/{id}",
	"GET /api/traefik/access-log-settings",
	"PUT /api/traefik/access-log-settings",
	"GET /api/traefik/access-logs",
	"GET /api/traefik/access-logs/{name}",
	"GET /api/traefik/config",
	"GET /api/traefik/config/apply",
	"POST /api/traefik/config/apply",
	"POST /api/traefik/config/apply/ack",
	"GET /api/traefik/config/history",
	"POST /api/traefik/config/history/{name}/restore",
	"POST /api/traefik/config/preview",
	"POST /api/traefik/config/test",
	"GET /api/traefik/logs",
	"POST /api/traefik/restart",
	"GET /api/traefik/status",
	"GET /api/vulnerabilities/settings",
	"PUT /api/vulnerabilities/settings",
	"GET /api/vulnerabilities/scanner-tags",
	"GET /api/vulnerabilities/status",
	"POST /api/vulnerabilities/scans",
	"POST /api/vulnerabilities/db-update",
	"DELETE /api/vulnerabilities/cache",
	"GET /api/vulnerabilities/images",
	"GET /api/vulnerabilities/images/{id}/findings",
	"GET /api/vulnerabilities/cves",
	"GET /api/vulnerabilities/summary",
}

// registeredPatterns scans the source for the patterns the panel's main mux
// registers: the first argument of every Handle/HandleFunc call in a
// function named RegisterRoutes or NewHandler, evaluated through string
// literals, `+`, parentheses and const identifiers. RegisterControlRoutes
// is left out: the control socket has no session. Scanning the source finds
// routes a running mux registers only under some Deps.
func registeredPatterns(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	byDir := map[string][]*ast.File{}
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, files := range byDir {
		consts := map[string]ast.Expr{}
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				if decl, ok := n.(*ast.GenDecl); ok && decl.Tok == token.CONST {
					for _, spec := range decl.Specs {
						vs := spec.(*ast.ValueSpec)
						for i, name := range vs.Names {
							if i < len(vs.Values) {
								consts[name.Name] = vs.Values[i]
							}
						}
					}
				}
				return true
			})
		}
		var eval func(ast.Expr) (string, bool)
		eval = func(e ast.Expr) (string, bool) {
			switch v := e.(type) {
			case *ast.BasicLit:
				s, err := strconv.Unquote(v.Value)
				return s, err == nil
			case *ast.Ident:
				if c, ok := consts[v.Name]; ok {
					return eval(c)
				}
			case *ast.BinaryExpr:
				a, okA := eval(v.X)
				b, okB := eval(v.Y)
				return a + b, okA && okB && v.Op == token.ADD
			case *ast.ParenExpr:
				return eval(v.X)
			}
			return "", false
		}
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || (fn.Name.Name != "RegisterRoutes" && fn.Name.Name != "NewHandler") {
					continue
				}
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") || len(call.Args) == 0 {
						return true
					}
					pattern, ok := eval(call.Args[0])
					if !ok {
						t.Errorf("%s: cannot evaluate the route pattern", fset.Position(call.Pos()))
						return true
					}
					seen[pattern] = true
					return true
				})
			}
		}
	}
	out := make([]string, 0, len(seen))
	for pattern := range seen {
		out = append(out, pattern)
	}
	sort.Strings(out)
	if len(out) < 100 {
		t.Fatalf("scanned only %d patterns; the scan is broken", len(out))
	}
	return out
}

func TestEveryRouteIsClassified(t *testing.T) {
	admin := map[string]bool{}
	for _, pattern := range adminOnly {
		admin[pattern] = true
		if _, ok := Routes[pattern]; ok {
			t.Errorf("%q is both in Routes and adminOnly", pattern)
		}
	}
	for _, pattern := range registeredPatterns(t) {
		if _, ok := Routes[pattern]; !ok && !admin[pattern] {
			t.Errorf("route %q is not classified: add it to authz.Routes, or to adminOnly in this test", pattern)
		}
	}
}

func TestRoutesNameRegisteredPatterns(t *testing.T) {
	registered := map[string]bool{}
	for _, pattern := range registeredPatterns(t) {
		registered[pattern] = true
	}
	for pattern := range Routes {
		if !registered[pattern] {
			t.Errorf("authz.Routes names %q, which no RegisterRoutes registers", pattern)
		}
	}
	for _, pattern := range adminOnly {
		if !registered[pattern] {
			t.Errorf("adminOnly names %q, which no RegisterRoutes registers", pattern)
		}
	}
}

func TestScopedRulesHaveTheirWildcard(t *testing.T) {
	hasProject := func(p string) bool {
		return strings.Contains(p, "{projectID}") || strings.Contains(p, "/projects/{id}")
	}
	for pattern, rule := range Routes {
		switch rule.Level {
		case LevelViewer, LevelProjectEditor:
			if !hasProject(pattern) {
				t.Errorf("%q is scoped but names no Project", pattern)
			}
		case LevelOperator, LevelEditor:
			if !hasProject(pattern) || !strings.Contains(pattern, "{environmentID}") {
				t.Errorf("%q needs a role in an Environment but names none", pattern)
			}
		}
	}
}
