package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// paths answers where a host path really is and whether it may be used.
type paths struct {
	composeDir []string // as written and with symlinks resolved
	roots      []string // composeDir plus the bind roots, both ways
	private    []string // the panel's own files inside the Compose directory
}

func newPaths(r Rules) *paths {
	p := &paths{}
	for _, dir := range both(r.ComposeDir) {
		p.composeDir = append(p.composeDir, dir)
		// A Source's git key lives in .ssh (sources.git).
		p.private = append(p.private, filepath.Join(dir, ".ssh"))
	}
	p.roots = append(p.roots, p.composeDir...)
	for _, root := range r.Roots {
		p.roots = append(p.roots, both(root)...)
	}
	return p
}

// both is path cleaned, and also with its symlinks resolved when that
// differs.
func both(path string) []string {
	clean := filepath.Clean(path)
	if resolved := resolve(clean); resolved != clean {
		return []string{clean, resolved}
	}
	return []string{clean}
}

// resolve follows every symlink in path's longest existing prefix; the rest
// does not exist yet, so it holds no link.
func resolve(path string) string {
	for prefix := path; ; prefix = filepath.Dir(prefix) {
		if real, err := filepath.EvalSymlinks(prefix); err == nil {
			rest, _ := filepath.Rel(prefix, path)
			return filepath.Join(real, rest)
		}
		if prefix == filepath.Dir(prefix) {
			return path
		}
	}
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func withinAny(path string, dirs []string) bool {
	for _, dir := range dirs {
		if within(path, dir) {
			return true
		}
	}
	return false
}

func isAbs(path string) bool { return filepath.IsAbs(path) }

func join(dir, path string) string { return filepath.Join(dir, path) }

// file checks a host path a service reads or binds: inside the Compose
// directory or a root, as written and after symlinks, and not the Compose
// directory itself or the panel's own files in it. It reports whether the
// path passed.
func (c *checker) file(service, what, path string) bool {
	if path == "" {
		return true
	}
	if !isAbs(path) {
		c.add(service, "%s %s is not an absolute path", what, path)
		return false
	}
	clean := filepath.Clean(path)
	for _, candidate := range []string{clean, resolve(clean)} {
		if !withinAny(candidate, c.paths.roots) {
			c.add(service, "%s %s is outside the Compose directory and the allowed roots", what, path)
			return false
		}
		for _, dir := range c.paths.composeDir {
			if candidate == dir {
				c.add(service, "%s %s is the whole Compose directory", what, path)
				return false
			}
		}
		if withinAny(candidate, c.paths.private) {
			c.add(service, "%s %s is the panel's own files", what, path)
			return false
		}
	}
	return true
}

// inCompose checks a build path: inside the Compose directory only.
func (c *checker) inCompose(service, what, path string) {
	if !isAbs(path) {
		c.add(service, "%s %s is not an absolute path", what, path)
		return
	}
	clean := filepath.Clean(path)
	for _, candidate := range []string{clean, resolve(clean)} {
		if !withinAny(candidate, c.paths.composeDir) {
			c.add(service, "%s %s is outside the Compose directory", what, path)
			return
		}
		if withinAny(candidate, c.paths.private) {
			c.add(service, "%s %s is the panel's own files", what, path)
			return
		}
	}
}

// nested refuses a bind inside another bind of the same Compose: a
// container that can write the outer one could swap the inner one for a
// symlink before the next up. The same path bound twice is not nested.
func (c *checker) nested() {
	for _, inner := range c.binds {
		for _, outer := range c.binds {
			a, b := resolve(filepath.Clean(inner.path)), resolve(filepath.Clean(outer.path))
			if a != b && within(a, b) {
				c.add(inner.service, "bind %s is inside bind %s", inner.path, outer.path)
				break
			}
		}
	}
}

// stored checks include and extends in the YAML as saved: config merges
// them away, and a file outside the Compose directory is read on the host.
func (c *checker) stored(content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var doc struct {
		Include  []any                     `yaml:"include"`
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return fmt.Errorf("read compose yaml: %w", err)
	}
	for _, entry := range doc.Include {
		switch e := entry.(type) {
		case string:
			c.reference("", "include", e)
		case map[string]any:
			switch p := e["path"].(type) {
			case string:
				c.reference("", "include", p)
			case []any:
				for _, item := range p {
					s, _ := item.(string)
					c.reference("", "include", s)
				}
			}
			for _, key := range []string{"env_file", "project_directory"} {
				switch v := e[key].(type) {
				case string:
					c.reference("", "include "+key, v)
				case []any:
					for _, item := range v {
						s, _ := item.(string)
						c.reference("", "include "+key, s)
					}
				}
			}
		}
	}
	for name, service := range doc.Services {
		if extends, ok := service["extends"].(map[string]any); ok {
			if file, ok := extends["file"].(string); ok {
				c.reference(name, "extends", file)
			}
		}
	}
	return nil
}

// reference allows a path relative to the Compose directory that stays in
// it and holds no variable.
func (c *checker) reference(service, what, path string) {
	switch {
	case strings.Contains(path, "://"), strings.HasPrefix(path, "git@"):
		c.add(orTop(service), "%s %s is fetched from elsewhere", what, path)
	case strings.Contains(path, "$"):
		c.add(orTop(service), "%s %s uses a variable", what, path)
	case isAbs(path) || path == ".." || strings.HasPrefix(filepath.Clean(path), ".."+string(os.PathSeparator)):
		c.add(orTop(service), "%s %s is outside the Compose directory", what, path)
	}
}

func orTop(service string) string {
	if service == "" {
		return "(file)"
	}
	return service
}
