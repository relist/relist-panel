package traefikconfig

import "strings"

// staticSections are the top-level fields of Traefik's static Configuration,
// from github.com/traefik/traefik/v3 tag v3.7.13,
// pkg/config/static/static_config.go. Traefik ignores an unknown top-level key
// without a word (but fails on an unknown nested one), so a typo there would
// silently do nothing; the merge refuses it instead.
var staticSections = []string{
	"global",
	"serversTransport",
	"tcpServersTransport",
	"entryPoints",
	"providers",
	"api",
	"metrics",
	"ping",
	"log",
	"accessLog",
	"tracing",
	"hostResolver",
	"certificatesResolvers",
	"experimental",
	"core",
	"spiffe",
	"ocsp",
}

func isStaticSection(key string) bool {
	for _, s := range staticSections {
		if strings.EqualFold(s, key) {
			return true
		}
	}
	return false
}
