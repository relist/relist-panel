// Package domains turns a Compose's domains into Traefik Docker labels.
package domains

import (
	"fmt"
	"time"
)

// Domain routes one host and path to one port of one Service of a Compose.
type Domain struct {
	ID           int64     `json:"id"`
	ComposeID    int64     `json:"-"`
	Host         string    `json:"host"`
	Path         string    `json:"path"`
	InternalPath string    `json:"internalPath"`
	StripPath    bool      `json:"stripPath"`
	Service      string    `json:"service"`
	Port         int       `json:"port"`
	Entrypoint   string    `json:"entrypoint"`
	HTTPS        bool      `json:"https"`
	CertProvider string    `json:"certProvider"` // "none" | "letsencrypt" | "custom"
	CertResolver string    `json:"certResolver"`
	Middlewares  []string  `json:"middlewares"` // never nil on the wire
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// RouterName is the name of the Traefik router, service and middleware
// prefix of a domain, e.g. "relist-c12-d3".
func RouterName(composeID, domainID int64) string {
	return fmt.Sprintf("relist-c%d-d%d", composeID, domainID)
}

// Address is the host and path a domain answers on, e.g. "api.example.com/".
func (d Domain) Address() string {
	path := d.Path
	if path == "" {
		path = "/"
	}
	return d.Host + path
}

// TargetError refuses a deploy: the domain's Service is missing or sets
// network_mode.
type TargetError struct {
	Domain Domain
	Reason string
}

func (e *TargetError) Error() string {
	return fmt.Sprintf("domain %s points at service %q, which %s", e.Domain.Address(), e.Domain.Service, e.Reason)
}
