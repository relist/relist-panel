package authz

import (
	"regexp"
	"strings"
	"testing"
)

func TestEveryChangeHasAnAction(t *testing.T) {
	registered := map[string]bool{}
	for _, pattern := range registeredPatterns(t) {
		registered[pattern] = true
		rule, classified := Routes[pattern]
		if strings.HasPrefix(pattern, "GET ") || !strings.Contains(pattern, " ") || classified && rule.Level == LevelPublic {
			continue
		}
		if _, ok := Actions[pattern]; !ok {
			t.Errorf("%q changes something but has no audit action: add it to authz.Actions", pattern)
		}
	}
	name := regexp.MustCompile(`^[a-z_0-9]+\.[a-z_0-9]+$`)
	gets := map[string]bool{}
	for pattern, action := range Actions {
		if !registered[pattern] {
			t.Errorf("authz.Actions names %q, which no RegisterRoutes registers", pattern)
		}
		if !name.MatchString(action) {
			t.Errorf("action %q of %q is not <resource>.<verb>", action, pattern)
		}
		if rule, ok := Routes[pattern]; ok && rule.Level == LevelPublic {
			t.Errorf("%q is Public; auth records it itself", pattern)
		}
		if strings.HasPrefix(pattern, "GET ") {
			gets[pattern] = true
		}
	}
	for _, want := range []string{"GET " + compose + "/volumes/read", "GET " + compose + "/volumes/archive", "GET /api/traefik/access-logs/{name}"} {
		if !gets[want] {
			t.Errorf("sensitive read %q has no action", want)
		}
		delete(gets, want)
	}
	for extra := range gets {
		t.Errorf("GET %q has an action, but only the spec's sensitive reads are recorded", extra)
	}
}

func TestKeyActions(t *testing.T) {
	for pattern, want := range map[string]string{
		"PUT " + compose:                                   "compose.update",
		"POST " + compose + "/deployments":                 "deployment.create",
		"POST " + compose + "/deployments/{number}/cancel": "deployment.cancel",
		"POST " + compose + "/restart":                     "compose.restart",
		"PUT /api/users/{id}/grants":                       "grant.set",
		"PUT /api/projects/{id}/grants":                    "grant.set",
		"POST /api/users":                                  "user.create",
		"POST /api/users/{id}/reset":                       "user.reset",
		"PUT /api/audit/settings":                          "audit_settings.update",
	} {
		if got := Actions[pattern]; got != want {
			t.Errorf("Actions[%q] = %q, want %q", pattern, got, want)
		}
	}
}
