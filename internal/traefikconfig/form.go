package traefikconfig

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Form is the Settings form: six fields, each one place in the Traefik
// override.
type Form struct {
	ACMEEmail       string   `json:"acmeEmail"`
	TrustedIPs      []string `json:"trustedIps"`
	TrustCloudflare bool     `json:"trustCloudflare"`
	LogLevel        string   `json:"logLevel"`
	AccessLog       bool     `json:"accessLog"`
	// ReadTimeout is web's and websecure's request read timeout. Empty on
	// write leaves the override's value as it is.
	ReadTimeout string `json:"readTimeout"`
}

// DefaultReadTimeout is base.yml's read timeout: an override that sets the
// same duration has no key.
const DefaultReadTimeout = "3600s"

const readTimeoutProblem = "read timeout must be a duration of at least 30s, like 90s, 5m or 1h"

// What Settings says about a read timeout it cannot show.
const (
	readTimeoutNull        = "read timeout is null here, which passes over base's 3600s; saving Settings removes it"
	readTimeoutNotDuration = "read timeout is not a duration; saving Settings replaces it with the value shown"
)

// LogLevels are the levels the form offers.
var LogLevels = []string{"ERROR", "WARN", "INFO", "DEBUG"}

var (
	emailPath   = []string{"certificatesResolvers", "letsencrypt", "acme", "email"}
	levelPath   = []string{"log", "level"}
	trustedTail = []string{"forwardedHeaders", "trustedIPs"}
	timeoutTail = []string{"transport", "respondingTimeouts", "readTimeout"}
	entryPoints = []string{"web", "websecure"}
)

const listsDiffer = "web and websecure differ; saving here makes them the same"

// ReadForm reads the form's fields from a Traefik override. The trusted IPs
// are websecure's; a warning says when web's differ.
func ReadForm(override []byte) (Form, []Problem, error) {
	m, err := parseMap(override)
	if err != nil {
		return Form{}, nil, err
	}
	if err := noDuplicateKeys(m); err != nil {
		return Form{}, nil, err
	}
	f := Form{TrustedIPs: []string{}, LogLevel: "INFO"}
	if _, v := lookup(m, emailPath...); v != nil && v.Kind == yaml.ScalarNode {
		f.ACMEEmail = v.Value
	}
	if _, v := lookup(m, levelPath...); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
		f.LogLevel = strings.ToUpper(v.Value)
	}
	if _, v := lookup(m, "accessLog"); v != nil {
		f.AccessLog = true
	}
	web, webOK := ipList(m, "web")
	secure, secureOK := ipList(m, "websecure")
	list := secure
	if !secureOK {
		list = web
	}
	var warnings []Problem
	if webOK && secureOK && !slices.Equal(web, secure) {
		warnings = append(warnings, Problem{Key: "entryPoints.web.forwardedHeaders.trustedIPs", Message: listsDiffer})
	}
	for _, ep := range entryPoints {
		key := "entryPoints." + ep + "." + strings.Join(timeoutTail, ".")
		switch _, v := lookup(m, append([]string{"entryPoints", ep}, timeoutTail...)...); {
		case v == nil:
		case isNull(v):
			warnings = append(warnings, Problem{Key: key, Message: readTimeoutNull})
		case v.Kind != yaml.ScalarNode:
			warnings = append(warnings, Problem{Key: key, Message: readTimeoutNotDuration})
		}
	}
	f.ReadTimeout = DefaultReadTimeout
	webTimeout, webTimeoutOK := readTimeout(m, "web")
	secureTimeout, secureTimeoutOK := readTimeout(m, "websecure")
	if secureTimeoutOK {
		f.ReadTimeout = secureTimeout
	} else if webTimeoutOK {
		f.ReadTimeout = webTimeout
	}
	// A missing key is base's value: one entrypoint set by hand still differs
	// from the other, and saving the form would copy it across.
	if !sameTimeout(webTimeout, webTimeoutOK, secureTimeout, secureTimeoutOK) {
		warnings = append(warnings, Problem{Key: "entryPoints.web." + strings.Join(timeoutTail, "."), Message: listsDiffer})
	}
	f.TrustCloudflare = len(list) > 0 && containsAll(list, CloudflareRanges)
	for _, ip := range list {
		if !f.TrustCloudflare || !slices.Contains(CloudflareRanges, ip) {
			f.TrustedIPs = append(f.TrustedIPs, ip)
		}
	}
	return f, warnings, nil
}

// WriteForm writes the form's fields into a Traefik override, editing its
// nodes in place so comments and the order of other keys survive.
func WriteForm(override []byte, f Form) ([]byte, []Problem, error) {
	var problems []Problem
	if !slices.Contains(LogLevels, strings.ToUpper(f.LogLevel)) {
		problems = append(problems, Problem{Key: "log.level", Message: fmt.Sprintf("log level must be one of %s", strings.Join(LogLevels, ", "))})
	}
	for _, ip := range f.TrustedIPs {
		if !ValidIP(strings.TrimSpace(ip)) {
			problems = append(problems, Problem{Key: "entryPoints.websecure.forwardedHeaders.trustedIPs",
				Message: fmt.Sprintf("%q is not an IP address or CIDR", ip)})
		}
	}
	timeout, timeoutText := time.Duration(0), ""
	// A value left as Settings shows it is not checked or rewritten, so a
	// hand-written one keeps its spelling, comment and shape.
	if raw := strings.TrimSpace(f.ReadTimeout); raw != "" && !sameAsShown(override, raw) {
		var ok bool
		if timeout, timeoutText, ok = parseReadTimeout(raw); !ok {
			problems = append(problems, Problem{Key: "entryPoints.websecure." + strings.Join(timeoutTail, "."), Message: readTimeoutProblem})
		}
	}
	if len(problems) > 0 {
		return nil, problems, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(override, &doc); err != nil {
		return nil, nil, err
	}
	var prefix []byte // a comment-only override keeps its text above the new map
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		prefix = override
		if len(prefix) > 0 && prefix[len(prefix)-1] != '\n' {
			prefix = append(slices.Clip(prefix), '\n')
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, nil, errNotMap
	}
	if err := noDuplicateKeys(m); err != nil {
		return nil, nil, err
	}

	if email := strings.TrimSpace(f.ACMEEmail); email != "" {
		setPath(m, emailPath, scalar(email))
	} else {
		removePath(m, emailPath)
	}

	var list []string
	for _, ip := range f.TrustedIPs {
		if ip = strings.TrimSpace(ip); ip != "" && !slices.Contains(list, ip) {
			list = append(list, ip)
		}
	}
	if f.TrustCloudflare {
		for _, r := range CloudflareRanges {
			if !slices.Contains(list, r) {
				list = append(list, r)
			}
		}
	}
	for _, ep := range entryPoints {
		path := append([]string{"entryPoints", ep}, trustedTail...)
		if len(list) == 0 {
			removePath(m, path)
			continue
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, ip := range list {
			seq.Content = append(seq.Content, scalar(ip))
		}
		setPath(m, path, seq)
	}

	for _, ep := range entryPoints {
		path := append([]string{"entryPoints", ep}, timeoutTail...)
		if _, v := lookup(m, path...); v != nil && isNull(v) {
			removePath(m, path) // null would pass over base's 3600s
		}
	}
	if timeoutText != "" {
		for _, ep := range entryPoints {
			path := append([]string{"entryPoints", ep}, timeoutTail...)
			if timeout == time.Hour {
				removePath(m, path)
			} else {
				setPath(m, path, scalar(timeoutText))
			}
		}
	}

	level := strings.ToUpper(f.LogLevel)
	if _, v := lookup(m, levelPath...); v != nil || level != "INFO" {
		setPath(m, levelPath, scalar(level))
	}

	if _, v := lookup(m, "accessLog"); f.AccessLog && v == nil {
		m.Content = append(m.Content, scalar("accessLog"), &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle})
	} else if !f.AccessLog {
		removePath(m, []string{"accessLog"})
	}

	var buf bytes.Buffer
	buf.Write(prefix)
	if len(m.Content) > 0 {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return nil, nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, nil, err
		}
	}
	return buf.Bytes(), nil, nil
}

func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: s} }

// ipList is an entrypoint's trusted IPs, and whether the key is there.
func ipList(m *yaml.Node, ep string) ([]string, bool) {
	_, v := lookup(m, append([]string{"entryPoints", ep}, trustedTail...)...)
	if v == nil {
		return nil, false
	}
	var out []string
	for _, item := range v.Content {
		out = append(out, item.Value)
	}
	return out, true
}

// sameAsShown reports whether raw is the read timeout Settings shows for
// override, by text or by duration, and both entrypoints already hold it.
// While they differ (or one holds null or a non-duration), saving writes the
// value to both, as Settings' warning says.
func sameAsShown(override []byte, raw string) bool {
	shown, warnings, err := ReadForm(override)
	if err != nil {
		return false
	}
	tail := "." + strings.Join(timeoutTail, ".")
	if slices.ContainsFunc(warnings, func(p Problem) bool { return strings.HasSuffix(p.Key, tail) }) {
		return false
	}
	if raw == shown.ReadTimeout {
		return true
	}
	a, _, aOK := parseReadTimeout(raw)
	b, _, bOK := parseReadTimeout(shown.ReadTimeout)
	return aOK && bOK && a == b
}

// readTimeout is an entrypoint's read timeout in the override, and whether
// the key holds a value (null and non-scalar ones count as absent).
func readTimeout(m *yaml.Node, ep string) (string, bool) {
	_, v := lookup(m, append([]string{"entryPoints", ep}, timeoutTail...)...)
	if v == nil || v.Kind != yaml.ScalarNode || isNull(v) {
		return "", false
	}
	return v.Value, true
}

// sameTimeout reports whether web's and websecure's read timeouts mean the
// same duration, a missing key standing for DefaultReadTimeout. Values that
// do not parse compare as text.
func sameTimeout(web string, webOK bool, secure string, secureOK bool) bool {
	if !webOK {
		web = DefaultReadTimeout
	}
	if !secureOK {
		secure = DefaultReadTimeout
	}
	w, _, wOK := parseReadTimeout(strings.TrimSpace(web))
	s, _, sOK := parseReadTimeout(strings.TrimSpace(secure))
	if wOK && sOK {
		return w == s
	}
	return web == secure
}

// parseReadTimeout reads a Traefik duration: bare digits are seconds,
// anything else a Go duration. It returns the text to write ("120" becomes
// "120s") and false below 30 seconds.
func parseReadTimeout(s string) (time.Duration, string, bool) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 30 || n > int64(time.Duration(1<<62)/time.Second) {
			return 0, "", false
		}
		return time.Duration(n) * time.Second, s + "s", true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 30*time.Second {
		return 0, "", false
	}
	return d, s, true
}

func containsAll(list, want []string) bool {
	for _, w := range want {
		if !slices.Contains(list, w) {
			return false
		}
	}
	return true
}

// removePath deletes path from mapping m, then any map on the way it left
// empty.
func removePath(m *yaml.Node, path []string) {
	if len(path) == 0 || m.Kind != yaml.MappingNode {
		return
	}
	i := indexOf(m, path[0])
	if i < 0 {
		return
	}
	if len(path) > 1 {
		child := m.Content[i+1]
		removePath(child, path[1:])
		if child.Kind != yaml.MappingNode || len(child.Content) > 0 {
			return
		}
	}
	m.Content = append(m.Content[:i], m.Content[i+2:]...)
}
