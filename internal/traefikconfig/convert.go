package traefikconfig

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// overrideHeader opens every Traefik override the panel creates.
const overrideHeader = `# Traefik override: your part of Traefik's static config (ADR-0030).
# The panel merges it over its base into traefik.yml. Edit it on the Traefik
# page; keys the panel needs are locked there.
`

// listFields are leaf keys whose CLI value is a comma-separated list. Every
// other value is a YAML scalar.
var listFields = []string{"trustedIPs", "sans", "domains", "resolvers"}

// sectionParents are where a `--a.b=true` flag turns a section on (an empty
// map) rather than setting a boolean: `--api=true`, `--providers.docker=true`.
var sectionParents = []string{"providers", "metrics", "tracing", "experimental"}

// FromArgs converts the running Traefik's CLI flags into a Traefik override,
// leaving out what the base already says. A flag it cannot convert (no `=`, an
// index, a locked key with another value) becomes a `# not converted:` line
// and a warning.
func FromArgs(args []string) ([]byte, []Problem) {
	ref, _ := parseMap(baseYAML)
	al, _ := parseMap(accessLogYAML)
	mergeNodes(ref, al)

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var comments []string
	var warnings []Problem
	reject := func(arg, why string) {
		comments = append(comments, "# not converted: "+arg)
		warnings = append(warnings, Problem{Key: arg, Message: "not converted from the running Traefik: " + why})
	}
	for _, arg := range args {
		name, val, hasEq := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !strings.HasPrefix(arg, "--") || !hasEq || name == "" {
			reject(arg, "not a --name=value flag")
			continue
		}
		if strings.EqualFold(name, "configfile") {
			continue
		}
		if strings.ContainsAny(name, "[]") {
			reject(arg, "indexed flags are not converted")
			continue
		}
		path := canonical(ref, strings.Split(name, "."))
		if lk, ok := lockedAt(path); ok && !strings.EqualFold(val, lk.Value) {
			reject(arg, "it conflicts with the panel's "+strings.Join(lk.Path, "."))
			continue
		}
		_, refNode := lookup(ref, path...)
		if refNode != nil {
			if refNode.Kind == yaml.ScalarNode && strings.EqualFold(refNode.Value, val) {
				continue
			}
			if refNode.Kind == yaml.MappingNode && strings.EqualFold(val, "true") && !strings.EqualFold(path[0], "accessLog") {
				continue
			}
		}
		switch {
		case len(path) == 1 && strings.EqualFold(val, "false"):
			continue
		case strings.EqualFold(val, "true") && (len(path) == 1 || len(path) == 2 && hasFold(sectionParents, path[0])):
			setPath(out, path, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle})
		case hasFold(listFields, path[len(path)-1]):
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, item := range strings.Split(val, ",") {
				if item = strings.TrimSpace(item); item != "" {
					seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: item})
				}
			}
			setPath(out, path, seq)
		default:
			setPath(out, path, &yaml.Node{Kind: yaml.ScalarNode, Value: val})
		}
	}
	return render(out, comments), warnings
}

// Seed is a first Traefik override from .env: the ACME email and the trusted
// IPs (RELIST_ACME_EMAIL, TRAEFIK_TRUSTED_IPS), and the Access log only when
// accessLog is set.
func Seed(acmeEmail, trustedIPs string, accessLog bool) []byte {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if accessLog {
		out.Content = append(out.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "accessLog"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle})
	}
	if acmeEmail = strings.TrimSpace(acmeEmail); acmeEmail != "" {
		setPath(out, []string{"certificatesResolvers", "letsencrypt", "acme", "email"}, &yaml.Node{Kind: yaml.ScalarNode, Value: acmeEmail})
	}
	var ips []*yaml.Node
	for _, ip := range strings.Split(trustedIPs, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			ips = append(ips, &yaml.Node{Kind: yaml.ScalarNode, Value: ip})
		}
	}
	if len(ips) > 0 {
		for _, ep := range []string{"web", "websecure"} {
			setPath(out, []string{"entryPoints", ep, "forwardedHeaders", "trustedIPs"},
				&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: ips})
		}
	}
	return render(out, nil)
}

// render writes the override header, any comment lines, then the map (none
// when it is empty).
func render(m *yaml.Node, comments []string) []byte {
	var buf bytes.Buffer
	buf.WriteString(overrideHeader)
	for _, c := range comments {
		buf.WriteString(c + "\n")
	}
	if len(m.Content) == 0 {
		return buf.Bytes()
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{m}}); err != nil {
		panic(fmt.Sprintf("traefikconfig: encode an override: %v", err)) // only built nodes reach here
	}
	enc.Close()
	return buf.Bytes()
}

// canonical respells path with ref's keys where ref has them.
func canonical(ref *yaml.Node, path []string) []string {
	out := make([]string, len(path))
	cur := ref
	for i, p := range path {
		out[i] = p
		if cur == nil || cur.Kind != yaml.MappingNode {
			cur = nil
			continue
		}
		j := indexOf(cur, p)
		if j < 0 {
			cur = nil
			continue
		}
		out[i] = cur.Content[j].Value
		cur = cur.Content[j+1]
	}
	return out
}

func lockedAt(path []string) (lockedKey, bool) {
	for _, lk := range locked {
		if len(lk.Path) != len(path) {
			continue
		}
		same := true
		for i := range path {
			if !strings.EqualFold(lk.Path[i], path[i]) {
				same = false
				break
			}
		}
		if same {
			return lk, true
		}
	}
	return lockedKey{}, false
}

// setPath sets path in mapping m to v, making maps on the way (in place of a
// scalar if one is there).
func setPath(m *yaml.Node, path []string, v *yaml.Node) {
	cur := m
	for i, p := range path {
		j := indexOf(cur, p)
		if i == len(path)-1 {
			if j < 0 {
				cur.Content = append(cur.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: p}, v)
			} else {
				cur.Content[j+1] = v
			}
			return
		}
		if j < 0 {
			next := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			cur.Content = append(cur.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: p}, next)
			cur = next
			continue
		}
		if cur.Content[j+1].Kind != yaml.MappingNode {
			cur.Content[j+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		cur = cur.Content[j+1]
		cur.Style &^= yaml.FlowStyle
	}
}

func hasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
