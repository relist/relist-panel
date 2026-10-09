package composes

import (
	"fmt"
	"sort"
	"strings"

	"relist-panel/internal/domains"
	"relist-panel/internal/slug"
)

const (
	aliasSuffix   = "internal"
	maxDNSLabel   = 63
	maxDNSName    = 253
	joinDomain    = "domain"
	joinLabels    = "labels"
	joinExplicit  = "explicit"
	fallbackSlug  = "x"
	traefikNetKey = "traefik.docker.network"
)

// networkIdentity names a Compose for its relist-network aliases: the frozen
// deploy project name for the stable alias, and the current names for the
// readable one (ADR-0014).
type networkIdentity struct {
	DeployName  string
	Compose     string
	Environment string
	Project     string
}

// sharedNetworkJoins decides, from the YAML as written, which Services join
// the shared network and why (ADR-0014): a Domain targets it, it carries
// hand-written Traefik labels, or its YAML already lists the network.
// Services with network_mode never join.
func sharedNetworkJoins(services map[string]any, networkName string, ds []domains.Domain) map[string]string {
	targets := map[string]bool{}
	for _, d := range ds {
		targets[d.Service] = true
	}
	joins := map[string]string{}
	for name, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasMode := service["network_mode"]; hasMode {
			continue
		}
		switch {
		case targets[name]:
			joins[name] = joinDomain
		case domains.HasTraefikLabels(service):
			joins[name] = joinLabels
		case listsNetwork(service["networks"], networkName):
			joins[name] = joinExplicit
		}
	}
	return joins
}

func listsNetwork(networks any, networkName string) bool {
	switch value := networks.(type) {
	case []any:
		for _, item := range value {
			if entry, ok := item.(string); ok && entry == networkName {
				return true
			}
		}
	case map[string]any:
		_, ok := value[networkName]
		return ok
	}
	return false
}

// joinSharedNetwork attaches each joining Service to the shared network with
// its aliases, and points Traefik at that network for Services routed by
// hand-written labels. It returns the warnings for aliases it had to skip.
func joinSharedNetwork(services map[string]any, joins map[string]string, networkName string, id networkIdentity) []string {
	names := make([]string, 0, len(joins))
	for name := range joins {
		names = append(names, name)
	}
	sort.Strings(names)

	// Two Service names that slugify alike would share an alias; neither gets one.
	bySlug := map[string][]string{}
	for _, name := range names {
		bySlug[slug.Slugify(name)] = append(bySlug[slug.Slugify(name)], name)
	}

	var warnings []string
	readableParts, readableProblem := readableSuffix(id)
	for _, name := range names {
		service := services[name].(map[string]any)
		var aliases []string
		serviceSlug, problem := aliasPart("service name", name)
		if problem == "" && len(bySlug[serviceSlug]) > 1 {
			problem = fmt.Sprintf("its name matches %s once made DNS-safe", strings.Join(others(bySlug[serviceSlug], name), ", "))
		}
		if problem != "" {
			warnings = append(warnings, fmt.Sprintf("service %s: no relist-network aliases: %s", name, problem))
		} else {
			if alias, err := dnsName(serviceSlug, id.DeployName, aliasSuffix); err != "" {
				warnings = append(warnings, fmt.Sprintf("service %s: no stable alias: %s", name, err))
			} else {
				aliases = append(aliases, alias)
			}
			if readableProblem != "" {
				warnings = append(warnings, fmt.Sprintf("service %s: no readable alias: %s", name, readableProblem))
			} else if alias, err := dnsName(append([]string{serviceSlug}, readableParts...)...); err != "" {
				warnings = append(warnings, fmt.Sprintf("service %s: no readable alias: %s", name, err))
			} else {
				aliases = append(aliases, alias)
			}
		}

		service["networks"] = withSharedNetwork(service["networks"], networkName, aliases)
		if joins[name] == joinLabels {
			domains.EnsureLabel(service, traefikNetKey, networkName)
		}
	}
	return warnings
}

// readableSuffix is compose.environment.project.internal from the current
// names, or why it cannot be built.
func readableSuffix(id networkIdentity) ([]string, string) {
	parts := []string{}
	for _, part := range []struct{ what, name string }{
		{"compose name", id.Compose},
		{"environment name", id.Environment},
		{"project name", id.Project},
	} {
		value, problem := aliasPart(part.what, part.name)
		if problem != "" {
			return nil, problem
		}
		parts = append(parts, value)
	}
	return append(parts, aliasSuffix), ""
}

// aliasPart slugifies one name, refusing a name with no DNS-safe characters:
// it would slugify to the fallback and collide with every other such name.
func aliasPart(what, name string) (string, string) {
	value := slug.Slugify(name)
	if value == fallbackSlug && strings.ToLower(strings.TrimSpace(name)) != fallbackSlug {
		return "", fmt.Sprintf("the %s %q has no letters or digits a DNS name can use", what, name)
	}
	return value, ""
}

func dnsName(labels ...string) (string, string) {
	for _, label := range labels {
		if len(label) > maxDNSLabel {
			return "", fmt.Sprintf("%q is longer than %d characters", label, maxDNSLabel)
		}
	}
	name := strings.Join(labels, ".")
	if len(name) > maxDNSName {
		return "", fmt.Sprintf("%q is longer than %d characters", name, maxDNSName)
	}
	return name, ""
}

func others(names []string, name string) []string {
	var rest []string
	for _, other := range names {
		if other != name {
			rest = append(rest, other)
		}
	}
	return rest
}

// withSharedNetwork adds the shared network, with aliases, to a Service's
// networks. A Service with no networks keeps the Compose's default network
// beside it; a list becomes a map, because only the map form takes aliases.
func withSharedNetwork(existing any, networkName string, aliases []string) any {
	networks := map[string]any{}
	switch value := existing.(type) {
	case map[string]any:
		networks = value
	case []any:
		for _, item := range value {
			networks[fmt.Sprint(item)] = nil
		}
	default:
		networks["default"] = nil
	}
	if len(aliases) == 0 {
		if _, ok := networks[networkName]; !ok {
			networks[networkName] = nil
		}
		return networks
	}

	entry, ok := networks[networkName].(map[string]any)
	if !ok {
		entry = map[string]any{}
	}
	merged, _ := entry["aliases"].([]any)
	for _, alias := range aliases {
		merged = appendUnique(merged, alias)
	}
	entry["aliases"] = merged
	networks[networkName] = entry
	return networks
}
