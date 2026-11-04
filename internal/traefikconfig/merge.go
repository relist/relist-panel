package traefikconfig

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Problem is one reason a Traefik override cannot be used. Key is the dotted
// path ("" for a YAML syntax error); Line is the override's line, 0 if unknown.
type Problem struct {
	Key     string `json:"key"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// lockedKey is a key the panel needs: the override may repeat its value but
// not change it.
type lockedKey struct {
	Path  []string
	Value string
}

var locked = []lockedKey{
	{[]string{"entryPoints", "web", "address"}, ":80"},
	{[]string{"entryPoints", "websecure", "address"}, ":443"},
	{[]string{"providers", "docker", "exposedByDefault"}, "false"},
	{[]string{"providers", "file", "directory"}, "/letsencrypt/dynamic"},
	{[]string{"providers", "file", "watch"}, "true"},
	{[]string{"certificatesResolvers", "letsencrypt", "acme", "storage"}, "/letsencrypt/acme.json"},
	{[]string{"certificatesResolvers", "letsencrypt", "acme", "httpChallenge", "entryPoint"}, "web"},
	{[]string{"log", "noColor"}, "true"},
	{[]string{"log", "format"}, "common"},
	{[]string{"accessLog", "filePath"}, "/letsencrypt/access/access.log"},
	{[]string{"accessLog", "format"}, "json"},
	{[]string{"accessLog", "fields", "headers", "defaultMode"}, "drop"},
}

// errNotMap is a Traefik override whose top level is not a map.
var errNotMap = errors.New("the Traefik override must be a YAML map")

// Generate merges override over the base and returns traefik.yml, headed by
// GeneratedHeader. Problems (a locked key changed, an unknown top-level key, a
// trusted IP that is not an IP or CIDR) return a nil file. The error is only
// for YAML that does not parse; ParseErrorProblem turns it into a Problem.
func Generate(override []byte) ([]byte, []Problem, error) {
	ov, err := parseMap(override)
	if err != nil {
		return nil, nil, err
	}
	if problems := Validate(ov); len(problems) > 0 {
		return nil, problems, nil
	}
	base, err := parseMap(baseYAML)
	if err != nil {
		return nil, nil, fmt.Errorf("the embedded base: %w", err)
	}
	if _, v := lookup(ov, "accessLog"); v != nil {
		al, err := parseMap(accessLogYAML)
		if err != nil {
			return nil, nil, fmt.Errorf("the embedded accessLog: %w", err)
		}
		mergeNodes(base, al)
	}
	mergeNodes(base, ov)
	var buf bytes.Buffer
	buf.WriteString(GeneratedHeader + "\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{base}}); err != nil {
		return nil, nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), nil, nil
}

// Validate checks a parsed override: top-level keys, locked keys and trusted IPs.
func Validate(ov *yaml.Node) []Problem {
	var problems []Problem
	for i := 0; i+1 < len(ov.Content); i += 2 {
		k := ov.Content[i]
		if !isStaticSection(k.Value) {
			problems = append(problems, Problem{Key: k.Value, Line: k.Line,
				Message: fmt.Sprintf("%q is not a Traefik static config section", k.Value)})
		}
	}
	problems = append(problems, duplicateKeys(ov, "")...)
	for _, lk := range locked {
		if p, ok := scalarAncestor(ov, lk.Path); ok {
			problems = append(problems, p)
			continue
		}
		k, v := lookup(ov, lk.Path...)
		if v == nil {
			continue
		}
		if v.Kind != yaml.ScalarNode || !lockedValueEqual(v.Value, lk.Value) {
			problems = append(problems, Problem{Key: strings.Join(lk.Path, "."), Line: k.Line,
				Message: fmt.Sprintf("the panel needs %s to be %s", strings.Join(lk.Path, "."), lk.Value)})
		}
	}
	if _, eps := lookup(ov, "entryPoints"); eps != nil && eps.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(eps.Content); i += 2 {
			name := eps.Content[i].Value
			k, v := lookup(eps.Content[i+1], "forwardedHeaders", "trustedIPs")
			if v == nil {
				continue
			}
			key := "entryPoints." + name + ".forwardedHeaders.trustedIPs"
			problems = append(problems, checkIPs(key, k, v)...)
		}
	}
	return problems
}

// duplicateKeys finds keys in one map that differ only in case. Traefik reads
// them as one key and the merge keeps the last, so every check that looks at
// the first would be checking the wrong value.
func duplicateKeys(n *yaml.Node, at string) []Problem {
	var problems []Problem
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			for j := 0; j < i; j += 2 {
				if prev := n.Content[j]; strings.EqualFold(prev.Value, k.Value) {
					problems = append(problems, Problem{Key: at + k.Value, Line: k.Line,
						Message: fmt.Sprintf("%q and %q (line %d) are the same key to Traefik; keep one", k.Value, prev.Value, prev.Line)})
					break
				}
			}
			problems = append(problems, duplicateKeys(n.Content[i+1], at+k.Value+".")...)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			problems = append(problems, duplicateKeys(c, at)...)
		}
	}
	return problems
}

// noDuplicateKeys is the first of duplicateKeys as an error, for the form,
// which reads and writes one spelling of each key.
func noDuplicateKeys(m *yaml.Node) error {
	if problems := duplicateKeys(m, ""); len(problems) > 0 {
		return fmt.Errorf("line %d: %s", problems[0].Line, problems[0].Message)
	}
	return nil
}

// scalarAncestor reports a value on the way to a locked key that is neither a
// map nor null: the merge would put it in place of the base's map and drop the
// locked key with it.
func scalarAncestor(ov *yaml.Node, path []string) (Problem, bool) {
	cur := ov
	for i, p := range path[:len(path)-1] {
		j := indexOf(cur, p)
		if j < 0 {
			return Problem{}, false
		}
		k, v := cur.Content[j], cur.Content[j+1]
		if isNull(v) {
			return Problem{}, false
		}
		if v.Kind != yaml.MappingNode {
			key := strings.Join(path[:i+1], ".")
			return Problem{Key: key, Line: k.Line, Message: fmt.Sprintf("the panel needs %s to be a map: it holds %s", key, strings.Join(path, "."))}, true
		}
		cur = v
	}
	return Problem{}, false
}

// lockedValueEqual compares an override's value with a locked one. Paths are
// case-sensitive on Linux; Traefik reads booleans and words in any case.
func lockedValueEqual(got, want string) bool {
	if strings.HasPrefix(want, "/") {
		return got == want
	}
	return strings.EqualFold(got, want)
}

func checkIPs(key string, k, v *yaml.Node) []Problem {
	if isNull(v) {
		return nil
	}
	if v.Kind != yaml.SequenceNode {
		return []Problem{{Key: key, Line: k.Line, Message: "trusted IPs must be a list of IPs or CIDRs"}}
	}
	var problems []Problem
	for _, item := range v.Content {
		if !ValidIP(item.Value) {
			problems = append(problems, Problem{Key: key, Line: item.Line,
				Message: fmt.Sprintf("%q is not an IP address or CIDR", item.Value)})
		}
	}
	return problems
}

// ValidIP reports whether s is an IP address or a CIDR.
func ValidIP(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

var lineRe = regexp.MustCompile(`line (\d+)`)

// ParseErrorProblem turns Generate's YAML error into a Problem with its line.
func ParseErrorProblem(err error) Problem {
	p := Problem{Message: err.Error()}
	if m := lineRe.FindStringSubmatch(err.Error()); m != nil {
		p.Line, _ = strconv.Atoi(m[1])
	}
	return p
}

// parseMap parses b into its top-level mapping node. Empty or comment-only
// input is an empty map.
func parseMap(b []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errNotMap
	}
	return doc.Content[0], nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "" && n.Tag == "")
}

// lookup walks a mapping by keys, matching case-insensitively as Traefik does.
// It returns the last key node and its value, or nils.
func lookup(n *yaml.Node, path ...string) (*yaml.Node, *yaml.Node) {
	var key *yaml.Node
	cur := n
	for _, p := range path {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil, nil
		}
		i := indexOf(cur, p)
		if i < 0 {
			return nil, nil
		}
		key, cur = cur.Content[i], cur.Content[i+1]
	}
	return key, cur
}

// indexOf is the index of key's key node in mapping m, or -1.
func indexOf(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if strings.EqualFold(m.Content[i].Value, key) {
			return i
		}
	}
	return -1
}

// mergeNodes merges the mapping override into the mapping base. Maps merge
// recursively; lists and scalars replace; a null over a map keeps the map. A
// key already in base keeps base's spelling.
func mergeNodes(base, override *yaml.Node) {
	for i := 0; i+1 < len(override.Content); i += 2 {
		k, v := override.Content[i], override.Content[i+1]
		j := indexOf(base, k.Value)
		if j < 0 {
			blockStyle(v)
			base.Content = append(base.Content, k, v)
			continue
		}
		bv := base.Content[j+1]
		switch {
		case bv.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode:
			mergeNodes(bv, v)
		case bv.Kind == yaml.MappingNode && isNull(v):
		default:
			blockStyle(v)
			base.Content[j+1] = v
		}
	}
}

// blockStyle drops flow style from maps, so merged output reads like the base.
func blockStyle(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		blockStyle(c)
	}
}

// AccessLogOn reports whether a generated traefik.yml writes the Access log.
func AccessLogOn(effective []byte) bool {
	m, err := parseMap(effective)
	if err != nil {
		return false
	}
	_, v := lookup(m, "accessLog")
	return v != nil
}

// TrustedIPs is the websecure entrypoint's trusted IPs in a traefik.yml.
func TrustedIPs(effective []byte) []string {
	m, err := parseMap(effective)
	if err != nil {
		return nil
	}
	_, v := lookup(m, "entryPoints", "websecure", "forwardedHeaders", "trustedIPs")
	if v == nil || v.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(v.Content))
	for _, item := range v.Content {
		out = append(out, item.Value)
	}
	return out
}
