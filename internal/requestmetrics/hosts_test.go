package requestmetrics

import (
	"context"
	"testing"
	"time"

	"relist-panel/internal/domains"
)

func TestDomainHostsReload(t *testing.T) {
	calls := 0
	list := []domains.Domain{{ID: 3, ComposeID: 12, Host: "A.Test"}}
	now := t0
	h := NewDomainHosts(func(context.Context) ([]domains.Domain, error) {
		calls++
		return list, nil
	}, func() time.Time { return now })

	if host, ok := h.Lookup(12, 3); !ok || host != "a.test" {
		t.Fatalf("lookup = %q, %v", host, ok)
	}
	if _, ok := h.Lookup(12, 9); ok || calls != 1 {
		t.Fatalf("first miss: ok=%v calls=%d", ok, calls)
	}
	now = now.Add(5 * time.Second)
	h.Lookup(12, 9)
	if calls != 1 {
		t.Fatalf("miss within 10s reloaded: calls=%d", calls)
	}
	list = append(list, domains.Domain{ID: 9, ComposeID: 12, Host: "new.test"})
	now = now.Add(6 * time.Second)
	if host, ok := h.Lookup(12, 9); !ok || host != "new.test" || calls != 2 {
		t.Fatalf("miss after 10s: %q %v calls=%d", host, ok, calls)
	}
	list[0].Host = "moved.test"
	now = now.Add(31 * time.Second)
	if host, _ := h.Lookup(12, 3); host != "moved.test" || calls != 3 {
		t.Fatalf("after 30s: %q calls=%d", host, calls)
	}
}
