// Package domains turns a Compose's domains into Traefik Docker labels.
package domains

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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

// Input is what a client sends to create or replace a domain.
type Input struct {
	Host         string   `json:"host"`
	Path         string   `json:"path"`
	InternalPath string   `json:"internalPath"`
	StripPath    bool     `json:"stripPath"`
	Service      string   `json:"service"`
	Port         int      `json:"port"`
	Entrypoint   string   `json:"entrypoint"`
	HTTPS        bool     `json:"https"`
	CertProvider string   `json:"certProvider"`
	CertResolver string   `json:"certResolver"`
	Middlewares  []string `json:"middlewares"`
}

// FieldError names the first invalid field of an Input, by its JSON name.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

var (
	hostLabelPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	identPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	middlewarePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)
)

const maxMiddlewares = 20

// Validate normalises in and checks it against the spec's rules. services is
// the Compose's DeclaredServices. Values end up inside Traefik labels, so the
// rules are strict; "$" is refused in paths because docker compose
// interpolates it in label values.
func Validate(in Input, services []string) (Input, *FieldError) {
	in.Host = strings.ToLower(strings.TrimSpace(in.Host))
	if !validHost(in.Host) {
		return in, &FieldError{"host", "host must be a hostname such as app.example.com"}
	}
	if in.Path == "" {
		in.Path = "/"
	}
	if in.InternalPath == "" {
		in.InternalPath = "/"
	}
	if !validPath(in.Path) {
		return in, &FieldError{"path", "path must start with / and contain no backtick, $, space, ? or #"}
	}
	if !validPath(in.InternalPath) {
		return in, &FieldError{"internalPath", "internal path must start with / and contain no backtick, $, space, ? or #"}
	}
	if !contains(services, in.Service) {
		return in, &FieldError{"service", fmt.Sprintf("service %q is not a service of this compose that can be routed to", in.Service)}
	}
	if in.Port < 1 || in.Port > 65535 {
		return in, &FieldError{"port", "port must be between 1 and 65535"}
	}
	if in.Entrypoint != "" && !identPattern.MatchString(in.Entrypoint) {
		return in, &FieldError{"entrypoint", "entrypoint must be empty or 1-64 letters, digits, - or _"}
	}
	if !in.HTTPS || in.CertProvider == "" {
		in.CertProvider = "none"
	}
	switch in.CertProvider {
	case "none", "letsencrypt", "custom":
	default:
		return in, &FieldError{"certProvider", "certificate provider must be none, letsencrypt or custom"}
	}
	if !in.HTTPS {
		in.CertProvider = "none"
	}
	if in.CertProvider == "custom" {
		if !identPattern.MatchString(in.CertResolver) {
			return in, &FieldError{"certResolver", "certificate resolver is required: 1-64 letters, digits, - or _"}
		}
	} else {
		in.CertResolver = ""
	}
	if in.Middlewares == nil {
		in.Middlewares = []string{}
	}
	if len(in.Middlewares) > maxMiddlewares {
		return in, &FieldError{"middlewares", fmt.Sprintf("at most %d middlewares are allowed", maxMiddlewares)}
	}
	for _, m := range in.Middlewares {
		if !middlewarePattern.MatchString(m) {
			return in, &FieldError{"middlewares", fmt.Sprintf("middleware %q must be 1-128 letters, digits, . _ - or @", m)}
		}
	}
	return in, nil
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validPath(path string) bool {
	return strings.HasPrefix(path, "/") && len(path) <= 255 && !strings.ContainsAny(path, "`$ ?#")
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// DeclaredServices returns the sorted keys of services: in yamlContent,
// leaving out any Service that sets network_mode. It is never nil, and empty
// when the YAML does not parse.
func DeclaredServices(yamlContent string) []string {
	names := []string{}
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return names
	}
	for name, service := range doc.Services {
		if _, hasMode := service["network_mode"]; hasMode {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
