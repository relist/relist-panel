package domains

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"relist-panel/internal/authgroups"
)

const (
	reasonMissing     = "is not in the compose file"
	reasonNetworkMode = "sets network_mode and cannot take a domain"

	routerPrefix = "traefik.http.routers."
)

// Inject adds every domain's labels to its Service in services (the YAML
// "services" map, mutated in place) and returns warnings in a stable order.
// Domains are processed in ID order. network is the Docker network Traefik
// reaches the containers on. A *TargetError is returned, with services left
// untouched, when a domain's Service is missing or sets network_mode. The
// domains of a Service in zeroDowntime also get a retry middleware and the
// fast-dial transport (ADR-0023).
func Inject(services map[string]any, composeID int64, domains []Domain, network string, zeroDowntime map[string]bool) ([]string, error) {
	ordered := append([]Domain(nil), domains...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	for _, d := range ordered {
		service, ok := services[d.Service].(map[string]any)
		if !ok {
			return nil, &TargetError{Domain: d, Reason: reasonMissing}
		}
		if _, ok := service["network_mode"]; ok {
			return nil, &TargetError{Domain: d, Reason: reasonNetworkMode}
		}
	}

	// Hand-written routers are snapshotted here, but the label sets are mutated
	// before hostWarnings runs, so they do contain the panel's own routers by
	// then. The check skips them through the relist-c<ID>- prefix filter in
	// handWrittenRouters, not through the order of operations.
	handWritten := map[string]*labelSet{}
	for name, raw := range services {
		if service, ok := raw.(map[string]any); ok {
			handWritten[name] = readLabels(service["labels"])
		}
	}
	generated := fmt.Sprintf("relist-c%d-", composeID)

	var warnings []string
	prepared := map[string]*labelSet{}
	for _, d := range ordered {
		labels, seen := prepared[d.Service]
		if !seen {
			labels = handWritten[d.Service]
			prepared[d.Service] = labels
			warnings = append(warnings, prepareService(d.Service, labels, generated, network)...)
		}
		addDomain(labels, composeID, d, zeroDowntime[d.Service])
	}

	for name, labels := range prepared {
		services[name].(map[string]any)["labels"] = labels.value()
	}

	return append(warnings, hostWarnings(handWritten, generated, ordered)...), nil
}

// Generated is, per Service, every Traefik label Inject writes for the
// Compose's domains, with its value: what the Compose policy lets a Service
// carry (ADR-0032).
func Generated(composeID int64, domains []Domain, network string, zeroDowntime map[string]bool) map[string]map[string]string {
	ordered := append([]Domain(nil), domains...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	generated := fmt.Sprintf("relist-c%d-", composeID)
	sets := map[string]*labelSet{}
	for _, d := range ordered {
		labels, seen := sets[d.Service]
		if !seen {
			labels = readLabels(nil)
			sets[d.Service] = labels
			prepareService(d.Service, labels, generated, network)
		}
		addDomain(labels, composeID, d, zeroDowntime[d.Service])
	}
	out := make(map[string]map[string]string, len(sets))
	for name, labels := range sets {
		out[name] = labels.values
	}
	return out
}

// prepareService applies the overrides every Service with a domain gets and
// returns their warnings.
func prepareService(name string, labels *labelSet, generated, network string) []string {
	var warnings []string
	if old, ok := labels.get("traefik.enable"); ok && isFalse(old) {
		warnings = append(warnings, fmt.Sprintf("service %q: traefik.enable=false was overridden to true because a domain points at it", name))
	}
	labels.set("traefik.enable", "true")

	if old, ok := labels.get("traefik.docker.network"); ok && old != network {
		warnings = append(warnings, fmt.Sprintf("service %q: traefik.docker.network was %q, overridden to %s", name, old, network))
	}
	labels.set("traefik.docker.network", network)

	for _, router := range handWrittenRouters(labels, generated) {
		if _, ok := labels.get(routerPrefix + router + ".service"); !ok {
			warnings = append(warnings, fmt.Sprintf("service %q: router %q has no traefik.http.routers.%s.service label, so it now routes to a domain's service; add one", name, router, router))
		}
	}
	return warnings
}

// Rule is the Traefik rule a domain's router matches.
func Rule(d Domain) string {
	rule := "Host(`" + d.Host + "`)"
	if d.Path != "" && d.Path != "/" {
		rule += " && PathPrefix(`" + d.Path + "`)"
	}
	return rule
}

// EntrypointFor is the entrypoint a domain's router listens on: its own, or
// web for HTTP and websecure for HTTPS.
func EntrypointFor(d Domain) string {
	if d.Entrypoint != "" {
		return d.Entrypoint
	}
	if d.HTTPS {
		return "websecure"
	}
	return "web"
}

// addDomain writes the labels of one domain.
func addDomain(labels *labelSet, composeID int64, d Domain, zeroDowntime bool) {
	n := RouterName(composeID, d.ID)
	router := routerPrefix + n

	rule := Rule(d)
	entrypoint := EntrypointFor(d)

	var chain []string
	if d.AuthGroupID != nil {
		// First, so a rejected request never reaches the others.
		chain = append(chain, authgroups.MiddlewareName(*d.AuthGroupID)+"@file")
	}
	if d.StripPath && d.Path != "" && d.Path != "/" {
		chain = append(chain, n+"-strip")
		labels.set("traefik.http.middlewares."+n+"-strip.stripprefix.prefixes", d.Path)
	}
	if d.InternalPath != "" && d.InternalPath != "/" {
		chain = append(chain, n+"-prefix")
		labels.set("traefik.http.middlewares."+n+"-prefix.addprefix.prefix", d.InternalPath)
	}
	chain = append(chain, d.Middlewares...)
	if zeroDowntime {
		// Last, right in front of the service: only the dial needs retrying,
		// and a retried request must not pass strip or prefix twice.
		chain = append(chain, n+"-retry")
		labels.set("traefik.http.middlewares."+n+"-retry.retry.attempts", "4")
		labels.set("traefik.http.services."+n+".loadbalancer.serversTransport", "relist-fastdial@file")
	}

	labels.set(router+".rule", rule)
	labels.set(router+".entrypoints", entrypoint)
	if len(chain) > 0 {
		labels.set(router+".middlewares", strings.Join(chain, ","))
	}
	labels.set(router+".service", n)
	labels.set("traefik.http.services."+n+".loadbalancer.server.port", fmt.Sprint(d.Port))

	if d.HTTPS {
		labels.set(router+".tls", "true")
		switch d.CertProvider {
		case "letsencrypt":
			labels.set(router+".tls.certresolver", "letsencrypt")
		case "custom":
			labels.set(router+".tls.certresolver", d.CertResolver)
		}
		if d.Entrypoint == "" {
			labels.set(router+"-http.rule", rule)
			labels.set(router+"-http.entrypoints", "web")
			labels.set(router+"-http.middlewares", n+"-redirect")
			labels.set(router+"-http.service", n)
			labels.set("traefik.http.middlewares."+n+"-redirect.redirectscheme.scheme", "https")
			labels.set("traefik.http.middlewares."+n+"-redirect.redirectscheme.permanent", "true")
		}
	}
}

// hostWarnings reports hand-written routers in the Compose whose rule matches
// a host a domain manages.
func hostWarnings(handWritten map[string]*labelSet, generated string, ordered []Domain) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, d := range ordered {
		if !seen[d.Host] {
			seen[d.Host] = true
			hosts = append(hosts, d.Host)
		}
	}

	names := make([]string, 0, len(handWritten))
	for name := range handWritten {
		names = append(names, name)
	}
	sort.Strings(names)

	var warnings []string
	for _, name := range names {
		labels := handWritten[name]
		for _, router := range handWrittenRouters(labels, generated) {
			rule, _ := labels.get(routerPrefix + router + ".rule")
			for _, host := range hosts {
				if strings.Contains(strings.ToLower(rule), "host(`"+strings.ToLower(host)+"`)") {
					warnings = append(warnings, fmt.Sprintf("router %q also matches Host(`%s`), which a domain manages", router, host))
				}
			}
		}
	}
	return warnings
}

// handWrittenRouters returns the sorted names of routers in labels that the
// panel did not generate.
func handWrittenRouters(labels *labelSet, generated string) []string {
	found := map[string]bool{}
	for _, key := range labels.keys {
		rest, ok := strings.CutPrefix(key, routerPrefix)
		if !ok {
			continue
		}
		router, _, ok := strings.Cut(rest, ".")
		if !ok || router == "" || strings.HasPrefix(router, generated) {
			continue
		}
		found[router] = true
	}
	routers := make([]string, 0, len(found))
	for router := range found {
		routers = append(routers, router)
	}
	sort.Strings(routers)
	return routers
}

// labelSet is a Service's labels as an ordered key to value list, remembering
// whether they were written as a YAML list or map.
type labelSet struct {
	keys   []string
	values map[string]string
	raw    map[string]any // original map values, kept for keys left alone
	asMap  bool
}

func readLabels(raw any) *labelSet {
	set := &labelSet{values: map[string]string{}}
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			key, val, _ := strings.Cut(fmt.Sprint(item), "=")
			set.put(key, val)
		}
	case map[string]any:
		set.asMap = true
		set.raw = value
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			val := ""
			if value[key] != nil {
				val = fmt.Sprint(value[key])
			}
			set.put(key, val)
		}
	}
	return set
}

func (s *labelSet) put(key, val string) {
	if _, ok := s.values[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.values[key] = val
}

func (s *labelSet) get(key string) (string, bool) {
	val, ok := s.values[key]
	return val, ok
}

func (s *labelSet) set(key, val string) {
	s.put(key, val)
	if s.asMap {
		s.raw[key] = val
	}
}

// value is the labels in their original shape: a map stays a map, anything
// else becomes a list of key=value.
func (s *labelSet) value() any {
	if s.asMap {
		return s.raw
	}
	list := make([]any, 0, len(s.keys))
	for _, key := range s.keys {
		list = append(list, key+"="+s.values[key])
	}
	return list
}

// isFalse reports whether a traefik.enable label value reads as false the way
// Traefik parses it (false, False, 0, f, ...).
func isFalse(value string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && !b
}

// HasTraefikLabels reports whether a Service carries any label whose key
// starts with "traefik.", in list or map form.
func HasTraefikLabels(service map[string]any) bool {
	for _, key := range readLabels(service["labels"]).keys {
		if strings.HasPrefix(key, "traefik.") {
			return true
		}
	}
	return false
}

// EnsureLabel sets key=value on a Service unless the key is already there,
// keeping the labels' list or map shape. It reports whether it added it.
func EnsureLabel(service map[string]any, key, value string) bool {
	labels := readLabels(service["labels"])
	if _, ok := labels.get(key); ok {
		return false
	}
	if service["labels"] == nil {
		labels.asMap = false
	}
	labels.set(key, value)
	service["labels"] = labels.value()
	return true
}
