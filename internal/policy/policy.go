// Package policy decides whether a resolved Compose config could take the
// host (.scratch/multi-user/spec.md, Compose policy; ADR-0032). Check reads
// the output of `docker compose config --format json --no-env-resolution`
// with every profile on, so interpolation, include and extends cannot hide a
// value. The rules are an allowlist: a service key the checker does not know
// is a violation.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Violation is one reason a config needs approval. Message names the key and
// the path, never a value an environment or label carries.
type Violation struct {
	Service string `json:"service"`
	Message string `json:"message"`
	// Bind and Volume name the bind source or named volume key the
	// violation is about, for the Volume Explorer.
	Bind   string `json:"-"`
	Volume string `json:"-"`
}

// Rules is what a config is checked against.
type Rules struct {
	// ComposeDir holds the Compose file and its Source.
	ComposeDir string
	// Roots are the host paths outside ComposeDir a bind may use
	// (RELIST_POLICY_BIND_ROOTS).
	Roots []string
	// ProjectName is the Compose's `docker compose -p` name; a named volume
	// must be called <ProjectName>_<key>.
	ProjectName string
	// PanelDomain is the install's own domain, which no traefik label may name.
	PanelDomain string
	// SourceCopy is this Deployment's private copy of its Source
	// (<ComposeDir>/.deployment-sources/<copy>), where its paths point;
	// empty when it has none.
	SourceCopy string
}

// harmless are service keys that cannot reach the host.
var harmless = []string{
	"annotations", "attach", "blkio_config", "cap_drop", "command", "container_name",
	"cpu_count", "cpu_percent", "cpu_period", "cpu_quota", "cpu_rt_period", "cpu_rt_runtime",
	"cpu_shares", "cpus", "cpuset", "depends_on", "develop", "dns", "dns_opt",
	"dns_search", "domainname", "entrypoint", "environment", "expose", "external_links",
	"extra_hosts", "group_add", "healthcheck", "hostname", "image", "init", "links", "logging",
	"mac_address", "mem_limit", "mem_reservation", "mem_swappiness", "memswap_limit",
	"oom_kill_disable", "oom_score_adj", "pids_limit", "platform", "ports", "profiles",
	"pull_policy", "read_only", "restart", "scale", "shm_size", "stdin_open", "stop_grace_period",
	"stop_signal", "storage_opt", "tmpfs", "tty", "ulimits", "user", "working_dir",
}

// checked are service keys allowed with the rules below.
var checked = []string{
	"build", "deploy", "networks", "volumes", "volumes_from", "env_file", "label_file", "secrets", "configs", "labels",
	"network_mode", "pid", "ipc", "uts", "cgroup", "userns_mode", "privileged", "use_api_socket",
	"post_start", "pre_stop",
}

// forbidden are service keys that are a violation whenever they carry a value.
var forbidden = []string{
	"cap_add", "devices", "device_cgroup_rules", "security_opt", "sysctls", "cgroup_parent",
	"runtime", "gpus", "provider",
}

type checker struct {
	rules      Rules
	services   map[string]map[string]any
	top        map[string]any
	paths      *paths
	violations []Violation
	binds      []bindUse
}

type bindUse struct {
	service string
	path    string
}

func (c *checker) add(service, format string, args ...any) {
	c.violations = append(c.violations, Violation{Service: service, Message: fmt.Sprintf(format, args...)})
}

// Check answers the config's violations. stored is the Compose's YAML as
// saved, read only for include and extends, which config merges away.
func Check(config []byte, stored string, r Rules) ([]Violation, error) {
	var doc map[string]any
	if err := json.Unmarshal(config, &doc); err != nil {
		return nil, fmt.Errorf("read resolved config: %w", err)
	}
	c := &checker{rules: r, top: doc, services: map[string]map[string]any{}, paths: newPaths(r)}
	services, _ := doc["services"].(map[string]any)
	for name, raw := range services {
		service, _ := raw.(map[string]any)
		c.services[name] = service
	}
	if err := c.stored(stored); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(c.services))
	for name := range c.services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c.service(name, c.services[name])
	}
	c.nested()
	return c.violations, nil
}

func (c *checker) service(name string, s map[string]any) {
	keys := make([]string, 0, len(s))
	for key := range s {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := s[key]
		switch {
		case strings.HasPrefix(key, "x-"), slices.Contains(harmless, key):
		case slices.Contains(forbidden, key):
			if present(value) {
				c.add(name, "%s is not allowed", key)
			}
		case slices.Contains(checked, key):
			c.checkedKey(name, key, value)
		default:
			c.add(name, "%s is not a key the policy knows", key)
		}
	}
}

func (c *checker) checkedKey(name, key string, value any) {
	switch key {
	case "privileged", "use_api_socket":
		if value == true {
			c.add(name, "%s is not allowed", key)
		}
	case "network_mode", "pid", "ipc", "uts", "cgroup":
		c.namespace(name, key, value)
	case "userns_mode":
		if value == "host" {
			c.add(name, "userns_mode host is not allowed")
		}
	case "post_start", "pre_stop":
		for _, hook := range list(value) {
			if h, _ := hook.(map[string]any); h["privileged"] == true {
				c.add(name, "a privileged %s hook is not allowed", key)
			}
		}
	case "labels":
		c.labels(name, value)
	case "volumes":
		c.volumes(name, value)
	case "volumes_from":
		for _, entry := range list(value) {
			from, _ := entry.(string)
			if strings.HasPrefix(from, "container:") {
				c.add(name, "volumes_from another container is not allowed")
				continue
			}
			service, _, _ := strings.Cut(from, ":")
			if _, ok := c.services[service]; !ok {
				c.add(name, "volumes_from %s names nothing in this Compose", service)
			}
		}
	case "env_file":
		for _, entry := range list(value) {
			path := ""
			switch e := entry.(type) {
			case string:
				path = e
			case map[string]any:
				path, _ = e["path"].(string)
			}
			c.file(name, "env_file", path)
		}
	case "label_file":
		for _, entry := range list(value) {
			path, _ := entry.(string)
			c.file(name, "label_file", path)
		}
	case "secrets", "configs":
		c.references(name, key, value)
	case "build":
		c.build(name, value)
	case "deploy":
		c.deploy(name, value)
	case "networks":
		c.networks(name, value)
	}
}

// namespace refuses sharing the host's namespace, or another container's
// outside this Compose.
func (c *checker) namespace(name, key string, value any) {
	mode, _ := value.(string)
	switch {
	case mode == "host":
		c.add(name, "%s host is not allowed", key)
	case strings.HasPrefix(mode, "container:"):
		c.add(name, "%s of another container is not allowed", key)
	case strings.HasPrefix(mode, "service:"):
		if _, ok := c.services[strings.TrimPrefix(mode, "service:")]; !ok {
			c.add(name, "%s names a service outside this Compose", key)
		}
	}
}

func (c *checker) labels(name string, value any) {
	labels, _ := value.(map[string]any)
	if c.rules.PanelDomain == "" {
		return
	}
	for key, raw := range labels {
		text, _ := raw.(string)
		if strings.HasPrefix(key, "traefik.") && namesHost(text, c.rules.PanelDomain) {
			c.add(name, "label %s names the panel's own domain", key)
		}
	}
}

// namesHost reports whether text holds host as a whole host name, not as
// the end of a longer one.
func namesHost(text, host string) bool {
	text, host = strings.ToLower(text), strings.ToLower(host)
	for start := 0; ; {
		i := strings.Index(text[start:], host)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(host)
		if (i == 0 || !hostChar(text[i-1])) && (end == len(text) || !hostChar(text[end])) {
			return true
		}
		start = i + 1
	}
}

func hostChar(b byte) bool {
	return b == '.' || b == '-' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

func (c *checker) volumes(name string, value any) {
	for _, entry := range list(value) {
		mount, _ := entry.(map[string]any)
		kind, _ := mount["type"].(string)
		source, _ := mount["source"].(string)
		switch kind {
		case "bind":
			if c.file(name, "bind", source) {
				c.binds = append(c.binds, bindUse{service: name, path: source})
			}
		case "volume":
			if source != "" {
				c.namedVolume(name, source)
			}
		case "tmpfs":
		default:
			c.add(name, "a %s mount is not allowed", kind)
		}
	}
}

// namedVolume refuses a volume that can open a host path or another
// Compose's data.
func (c *checker) namedVolume(name, key string) {
	before := len(c.violations)
	defer func() {
		for i := before; i < len(c.violations); i++ {
			c.violations[i].Volume = key
		}
	}()
	volumes, _ := c.top["volumes"].(map[string]any)
	volume, ok := volumes[key].(map[string]any)
	if !ok {
		c.add(name, "volume %s is not declared", key)
		return
	}
	if driver, _ := volume["driver"].(string); driver != "" && driver != "local" {
		c.add(name, "volume %s uses driver %s", key, driver)
	}
	if present(volume["driver_opts"]) {
		c.add(name, "volume %s sets driver_opts", key)
	}
	if volume["external"] == true {
		c.add(name, "volume %s is external", key)
	}
	if want := c.rules.ProjectName + "_" + key; volume["name"] != want {
		c.add(name, "volume %s is not named %s", key, want)
	}
}

// references checks the top-level secrets or configs a service uses.
func (c *checker) references(name, key string, value any) {
	top, _ := c.top[key].(map[string]any)
	for _, entry := range list(value) {
		ref := ""
		switch e := entry.(type) {
		case string:
			ref = e
		case map[string]any:
			ref, _ = e["source"].(string)
		}
		def, _ := top[ref].(map[string]any)
		if def == nil {
			continue
		}
		if present(def["environment"]) {
			c.add(name, "%s %s is read from the panel's environment", strings.TrimSuffix(key, "s"), ref)
		}
		if file, ok := def["file"].(string); ok {
			c.file(name, strings.TrimSuffix(key, "s")+" "+ref, file)
		}
	}
}

// buildKeys are the build keys the policy knows; the ones that can reach
// the host are checked below.
var buildKeys = []string{
	"context", "dockerfile", "dockerfile_inline", "args", "labels", "target", "tags", "platforms",
	"pull", "no_cache", "shm_size", "extra_hosts", "cache_from", "cache_to", "additional_contexts",
	"network", "privileged", "entitlements", "secrets", "ssh", "ulimits", "provenance", "sbom",
}

func (c *checker) build(name string, value any) {
	build, _ := value.(map[string]any)
	if build == nil {
		return
	}
	for key := range build {
		if !slices.Contains(buildKeys, key) && !strings.HasPrefix(key, "x-") {
			c.add(name, "build %s is not a key the policy knows", key)
		}
	}
	for _, key := range []string{"cache_from", "cache_to"} {
		for _, entry := range list(build[key]) {
			// A local cache reads or writes a path on the Builder's host.
			if text, _ := entry.(string); strings.Contains(text, "type=local") {
				c.add(name, "build %s of type local is not allowed", key)
			}
		}
	}
	context, _ := build["context"].(string)
	local := context != "" && !remote(context)
	if local {
		c.inCompose(name, "build context", context)
	}
	if dockerfile, _ := build["dockerfile"].(string); dockerfile != "" && local {
		if !isAbs(dockerfile) {
			dockerfile = join(context, dockerfile)
		}
		c.inCompose(name, "dockerfile", dockerfile)
	}
	additional, _ := build["additional_contexts"].(map[string]any)
	for key, raw := range additional {
		path, _ := raw.(string)
		switch {
		case strings.HasPrefix(path, "oci-layout://"):
			c.inCompose(name, "additional context "+key, strings.TrimPrefix(path, "oci-layout://"))
		case strings.HasPrefix(path, "service:"), strings.HasPrefix(path, "target:"), remote(path):
		default:
			c.inCompose(name, "additional context "+key, path)
		}
	}
	if build["network"] == "host" {
		c.add(name, "build network host is not allowed")
	}
	if build["privileged"] == true {
		c.add(name, "a privileged build is not allowed")
	}
	if present(build["entitlements"]) {
		c.add(name, "build entitlements are not allowed")
	}
	if present(build["ssh"]) {
		c.add(name, "build ssh is not allowed")
	}
	if secrets, ok := build["secrets"]; ok {
		c.references(name, "secrets", secrets)
	}
}

// remote reports whether a build context is fetched rather than read from
// the host.
func remote(context string) bool {
	return strings.Contains(context, "://") || strings.HasPrefix(context, "git@") || strings.HasPrefix(context, "github.com/")
}

// present reports whether a value is set to something.
func present(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func list(value any) []any {
	l, _ := value.([]any)
	return l
}

// Hash is the SHA-256 of the config with its keys in a fixed order: what an
// approval is for.
func Hash(config []byte) (string, error) {
	var doc any
	if err := json.Unmarshal(config, &doc); err != nil {
		return "", fmt.Errorf("read resolved config: %w", err)
	}
	canonical, err := json.Marshal(doc) // map keys marshal sorted
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// deployKeys are the deploy keys the policy knows; resources may not
// reserve devices (another spelling of gpus and devices).
var deployKeys = []string{"mode", "replicas", "labels", "update_config", "rollback_config", "restart_policy", "placement", "resources", "endpoint_mode"}

func (c *checker) deploy(name string, value any) {
	deploy, _ := value.(map[string]any)
	for key := range deploy {
		if !slices.Contains(deployKeys, key) && !strings.HasPrefix(key, "x-") {
			c.add(name, "deploy %s is not a key the policy knows", key)
		}
	}
	resources, _ := deploy["resources"].(map[string]any)
	for _, kind := range []string{"limits", "reservations"} {
		limits, _ := resources[kind].(map[string]any)
		for key, v := range limits {
			if !slices.Contains([]string{"cpus", "memory", "pids"}, key) && present(v) {
				c.add(name, "deploy resources %s %s is not allowed", kind, key)
			}
		}
	}
}

// sharedNetwork is the panel's own network (ADR-0014), external by design.
const sharedNetwork = "relist-network"

// networks checks the top-level networks a service joins: this Compose's
// own bridges, or the panel's shared network.
func (c *checker) networks(name string, value any) {
	joined, _ := value.(map[string]any)
	top, _ := c.top["networks"].(map[string]any)
	for key := range joined {
		def, _ := top[key].(map[string]any)
		if def == nil {
			continue
		}
		if key == sharedNetwork && def["name"] == sharedNetwork && def["external"] == true {
			continue
		}
		if def["external"] == true {
			c.add(name, "network %s is external", key)
		}
		if driver, _ := def["driver"].(string); driver != "" && driver != "bridge" {
			c.add(name, "network %s uses driver %s", key, driver)
		}
		if present(def["driver_opts"]) {
			c.add(name, "network %s sets driver_opts", key)
		}
		if want := c.rules.ProjectName + "_" + key; def["name"] != want {
			c.add(name, "network %s is not named %s", key, want)
		}
	}
}
