package requestmetrics

import (
	"context"
	"strings"
	"sync"
	"time"

	"relist-panel/internal/domains"
)

const (
	hostsRefresh     = 30 * time.Second
	hostsMissRefresh = 10 * time.Second
)

// DomainHosts maps a Domain's ID to its host, read from the database. The
// map reloads when it is 30 seconds old and on a miss, at most once per 10
// seconds, so a Domain added by a deploy is found without a hook into
// deploys. The host is the one stored on the Domain, lower-cased; it never
// comes from a request.
type DomainHosts struct {
	list func(context.Context) ([]domains.Domain, error)
	now  func() time.Time

	mu       sync.Mutex
	hosts    map[[2]int64]string
	loadedAt time.Time
}

// NewDomainHosts constructs the lookup over a Domain lister.
func NewDomainHosts(list func(context.Context) ([]domains.Domain, error), now func() time.Time) *DomainHosts {
	return &DomainHosts{list: list, now: now}
}

// Lookup matches HostLookup.
func (h *DomainHosts) Lookup(composeID, domainID int64) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if h.loadedAt.IsZero() || now.Sub(h.loadedAt) >= hostsRefresh {
		h.reload(now)
	}
	key := [2]int64{composeID, domainID}
	if host, ok := h.hosts[key]; ok {
		return host, true
	}
	if now.Sub(h.loadedAt) >= hostsMissRefresh {
		h.reload(now)
		host, ok := h.hosts[key]
		return host, ok
	}
	return "", false
}

// reload keeps the old map when the list fails: a stale host beats none.
func (h *DomainHosts) reload(now time.Time) {
	h.loadedAt = now
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := h.list(ctx)
	if err != nil {
		return
	}
	m := make(map[[2]int64]string, len(list))
	for _, d := range list {
		m[[2]int64{d.ComposeID, d.ID}] = strings.ToLower(d.Host)
	}
	h.hosts = m
}
