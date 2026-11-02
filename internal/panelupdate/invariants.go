package panelupdate

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// An Install invariant is a fact about the merged Compose config that the
// Updater relies on (spec "Install invariant kinds"). The manifest only picks
// kinds and values; the code here checks them.

// composeConfig is the part of `docker compose config --format json` the
// evaluators read.
type composeConfig struct {
	Services map[string]composeService `json:"services"`
	Networks map[string]composeNetwork `json:"networks"`
}

type composeService struct {
	Image       string          `json:"image"`
	Build       json.RawMessage `json:"build"`
	Command     json.RawMessage `json:"command"`
	Environment json.RawMessage `json:"environment"`
	Labels      json.RawMessage `json:"labels"`
	Networks    json.RawMessage `json:"networks"`
	Volumes     []composeMount  `json:"volumes"`
}

type composeMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type composeNetwork struct {
	Name     string          `json:"name"`
	External json.RawMessage `json:"external"`
}

func parseComposeConfig(data []byte) (*composeConfig, error) {
	var c composeConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("could not read the merged config: %w", err)
	}
	return &c, nil
}

// invariantEnv is what an evaluator needs besides the config.
type invariantEnv struct {
	// Image is the manifest's digest ref.
	Image string
	// Vars are the candidate .env's values, for ${…} in an invariant's fields.
	Vars map[string]string
}

// outcome is one evaluated invariant.
type outcome struct {
	Text   string // what the invariant says
	OK     bool
	Detail string // why it fails
}

type evaluator func(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome

// invariantEvaluators is the closed set of kinds this build knows.
var invariantEvaluators = map[string]evaluator{
	"service-image":       evalServiceImage,
	"service-mount":       evalServiceMount,
	"service-command-has": evalServiceCommandHas,
	"service-label":       evalServiceLabel,
	"service-network":     evalServiceNetwork,
	"network-external":    evalNetworkExternal,
	"data-dir-same-path":  evalDataDirSamePath,
}

// InvariantKinds lists the invariant kinds this build can evaluate, sorted.
// Manifest validation uses it as the one source of known kinds.
func InvariantKinds() []string {
	kinds := make([]string, 0, len(invariantEvaluators))
	for k := range invariantEvaluators {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// UnknownKinds lists, in order and without repeats, the kinds in invariants
// that this build does not know.
func UnknownKinds(invariants []Invariant) []string {
	var unknown []string
	for _, inv := range invariants {
		if _, ok := invariantEvaluators[inv.Kind]; !ok && !slices.Contains(unknown, inv.Kind) {
			unknown = append(unknown, inv.Kind)
		}
	}
	return unknown
}

// evaluate runs one invariant of a known kind. A malformed one fails.
func evaluate(inv Invariant, c *composeConfig, env invariantEnv) outcome {
	return invariantEvaluators[inv.Kind](inv.Raw, c, env)
}

func malformed(kind string, err error) outcome {
	return outcome{Text: "Invariant " + kind, Detail: "the invariant is malformed: " + err.Error()}
}

func evalServiceImage(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct{ Service, Image string }
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("service-image", err)
	}
	// The manifest's image is the digest the Updater pulled; the field is
	// kept so a manifest can name it, and must agree.
	want := f.Image
	if want == "" {
		want = env.Image
	}
	want = expandVars(want, env.Vars)
	out := outcome{Text: fmt.Sprintf("Service %s runs %s", f.Service, want)}
	svc, ok := c.Services[f.Service]
	switch {
	case !ok:
		out.Detail = "there is no Service " + f.Service
	case hasValue(svc.Build):
		out.Detail = "Service " + f.Service + " builds its image instead of using the published one"
	case svc.Image != want:
		out.Detail = fmt.Sprintf("Service %s uses image %q", f.Service, svc.Image)
	default:
		out.OK = true
	}
	return out
}

func evalServiceMount(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct {
		Service, Source, Target string
		ReadOnly                bool `json:"read_only"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("service-mount", err)
	}
	source, target := expandVars(f.Source, env.Vars), expandVars(f.Target, env.Vars)
	text := fmt.Sprintf("Service %s mounts %s at %s", f.Service, source, target)
	if f.ReadOnly {
		text += " read-only"
	}
	out := outcome{Text: text}
	svc, ok := c.Services[f.Service]
	if !ok {
		out.Detail = "there is no Service " + f.Service
		return out
	}
	for _, m := range svc.Volumes {
		if m.Source == source && m.Target == target {
			if f.ReadOnly && !m.ReadOnly {
				out.Detail = "the mount is not read-only"
				return out
			}
			out.OK = true
			return out
		}
	}
	out.Detail = fmt.Sprintf("Service %s has no mount of %s at %s", f.Service, source, target)
	return out
}

func evalServiceCommandHas(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct{ Service, Arg string }
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("service-command-has", err)
	}
	arg := expandVars(f.Arg, env.Vars)
	out := outcome{Text: fmt.Sprintf("Service %s runs with %s", f.Service, arg)}
	svc, ok := c.Services[f.Service]
	switch {
	case !ok:
		out.Detail = "there is no Service " + f.Service
	case !slices.Contains(commandArgs(svc.Command), arg):
		out.Detail = fmt.Sprintf("the command of Service %s lacks %s", f.Service, arg)
	default:
		out.OK = true
	}
	return out
}

func evalServiceLabel(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct{ Service, Key, Value string }
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("service-label", err)
	}
	value := expandVars(f.Value, env.Vars)
	out := outcome{Text: fmt.Sprintf("Service %s has label %s=%s", f.Service, f.Key, value)}
	svc, ok := c.Services[f.Service]
	if !ok {
		out.Detail = "there is no Service " + f.Service
		return out
	}
	got, present := keyValues(svc.Labels)[f.Key]
	switch {
	case !present:
		out.Detail = fmt.Sprintf("Service %s has no label %s", f.Service, f.Key)
	case got != value:
		out.Detail = fmt.Sprintf("label %s is %q", f.Key, got)
	default:
		out.OK = true
	}
	return out
}

func evalServiceNetwork(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct{ Service, Network string }
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("service-network", err)
	}
	out := outcome{Text: fmt.Sprintf("Service %s is on network %s", f.Service, f.Network)}
	svc, ok := c.Services[f.Service]
	switch {
	case !ok:
		out.Detail = "there is no Service " + f.Service
	case !slices.Contains(networkNames(svc.Networks), f.Network):
		out.Detail = fmt.Sprintf("Service %s is not attached to %s", f.Service, f.Network)
	default:
		out.OK = true
	}
	return out
}

func evalNetworkExternal(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct{ Network, Name string }
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("network-external", err)
	}
	out := outcome{Text: fmt.Sprintf("Network %s is the external network %s", f.Network, f.Name)}
	n, ok := c.Networks[f.Network]
	switch {
	case !ok:
		out.Detail = "there is no network " + f.Network
	case !isTrue(n.External):
		out.Detail = fmt.Sprintf("network %s is not external", f.Network)
	case n.Name != f.Name:
		out.Detail = fmt.Sprintf("network %s is named %q", f.Network, n.Name)
	default:
		out.OK = true
	}
	return out
}

// evalDataDirSamePath holds when the data dir the Service is told about
// (RELIST_DATA_DIR) is mounted at the same path inside and outside.
func evalDataDirSamePath(raw json.RawMessage, c *composeConfig, env invariantEnv) outcome {
	var f struct{ Service string }
	if err := json.Unmarshal(raw, &f); err != nil {
		return malformed("data-dir-same-path", err)
	}
	out := outcome{Text: fmt.Sprintf("Service %s sees the data dir at its host path", f.Service)}
	svc, ok := c.Services[f.Service]
	if !ok {
		out.Detail = "there is no Service " + f.Service
		return out
	}
	dir := keyValues(svc.Environment)["RELIST_DATA_DIR"]
	if dir == "" {
		out.Detail = "Service " + f.Service + " has no RELIST_DATA_DIR"
		return out
	}
	for _, m := range svc.Volumes {
		if m.Source == dir && m.Target == dir {
			out.OK = true
			return out
		}
	}
	out.Detail = fmt.Sprintf("Service %s does not mount %s at the same path", f.Service, dir)
	return out
}

func hasValue(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "{}" && s != "[]" && s != `""`
}

func isTrue(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "true" }

// commandArgs reads a command, which `config` prints as a list but a string
// is accepted too.
func commandArgs(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var line string
	if json.Unmarshal(raw, &line) == nil {
		return strings.Fields(line)
	}
	return nil
}

// keyValues reads labels or environment, which are a map in `config` output
// and a list of KEY=VALUE in some inputs. A null value reads as empty.
func keyValues(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			} else if v == nil {
				out[k] = ""
			} else {
				out[k] = fmt.Sprint(v)
			}
		}
		return out
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		for _, kv := range list {
			k, v, _ := strings.Cut(kv, "=")
			out[k] = v
		}
	}
	return out
}

func networkNames(raw json.RawMessage) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		return names
	}
	var list []string
	_ = json.Unmarshal(raw, &list)
	return list
}

// expandVars expands ${VAR}, ${VAR:-default} and ${VAR-default} from vars.
func expandVars(s string, vars map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) || s[i+1] != '{' {
			b.WriteByte(s[i])
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		expr := s[i+2 : i+end]
		i += end
		b.WriteString(expandOne(expr, vars))
	}
	return b.String()
}

func expandOne(expr string, vars map[string]string) string {
	if name, def, ok := strings.Cut(expr, ":-"); ok {
		if v := vars[name]; v != "" {
			return v
		}
		return def
	}
	if name, def, ok := strings.Cut(expr, "-"); ok {
		if v, set := vars[name]; set {
			return v
		}
		return def
	}
	return vars[expr]
}
