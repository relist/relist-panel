package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// paths answers where a host path really is and whether it may be used.
type paths struct {
	composeDir []string // as written and with symlinks resolved
	roots      []string // composeDir plus the bind roots, both ways
	private    []string // a Source's git key, refused to every kind of path
	sourceCopy []string // this Deployment's private Source copy, both ways
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
	if r.SourceCopy != "" {
		p.sourceCopy = both(r.SourceCopy)
	}
	return p
}

// panelOwned reports whether path is a file the panel keeps in the Compose
// directory (anything named with a leading dot, the deployed file, the
// Variable Set files): a container that could write one could change what
// the next Deployment runs. This Deployment's own Source copy is not.
func (p *paths) panelOwned(path string) bool {
	if withinAny(path, p.sourceCopy) {
		return false
	}
	for _, dir := range p.composeDir {
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		first, _, _ := strings.Cut(rel, string(filepath.Separator))
		if first == "." || strings.HasPrefix(first, ".") || first == "docker-compose.yml" || first == "sets" {
			return true
		}
	}
	return false
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

// resolve follows every symlink in path; see resolveStrict.
func resolve(path string) string {
	real, _ := resolveStrict(path)
	return real
}

// resolveStrict follows every symlink in path, component by component. A
// component that does not exist ends the walk: the rest holds no link yet.
// ok is false when a link cannot be followed from the panel's side (its
// target is not there): dockerd on the host might follow it anywhere.
func resolveStrict(path string) (string, bool) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return clean, false
	}
	parts := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	for i, part := range parts {
		if part == "" {
			continue
		}
		next := filepath.Join(current, part)
		info, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.Join(append([]string{next}, parts[i+1:]...)...), true
		}
		if err != nil {
			return clean, false
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(next)
			if err != nil {
				return clean, false
			}
			next = real
		}
		current = next
	}
	return current, true
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
	if what == "bind" {
		before := len(c.violations)
		defer func() {
			for i := before; i < len(c.violations); i++ {
				c.violations[i].Bind = path
			}
		}()
	}
	if !isAbs(path) {
		c.add(service, "%s %s is not an absolute path", what, path)
		return false
	}
	clean := filepath.Clean(path)
	real, ok := resolveStrict(clean)
	if !ok {
		c.add(service, "%s %s goes through a link the panel cannot follow", what, path)
		return false
	}
	for _, candidate := range []string{clean, real} {
		if !withinAny(candidate, c.paths.roots) {
			c.add(service, "%s %s is outside the Compose directory and the allowed roots", what, path)
			return false
		}
		if what == "bind" && c.paths.panelOwned(candidate) {
			c.add(service, "%s %s is the panel's own files", what, path)
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
	real, ok := resolveStrict(clean)
	if !ok {
		c.add(service, "%s %s goes through a link the panel cannot follow", what, path)
		return
	}
	for _, candidate := range []string{clean, real} {
		if !withinAny(candidate, c.paths.composeDir) {
			c.add(service, "%s %s is outside the Compose directory", what, path)
			return
		}
		if withinAny(candidate, c.paths.private) || c.paths.panelOwned(candidate) {
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
				c.violations[len(c.violations)-1].Bind = inner.path
				break
			}
		}
	}
}

// stored checks include and extends in the YAML as saved: config merges
// them away, and a file outside the Compose directory is read on the host.
func (c *checker) stored(content string) error {
	return c.storedFile(content, c.rules.ComposeDir, 0)
}

// maxIncludeDepth bounds how far include and extends are followed.
const maxIncludeDepth = 8

// follow checks the file an include or extends names, relative to base,
// when the reference itself passed: its own include and extends are read
// on the host too. A Deployment reads Source files from its own copy.
func (c *checker) follow(service, what, path, base string, depth int) {
	before := len(c.violations)
	c.reference(service, what, path)
	if len(c.violations) > before || depth >= maxIncludeDepth {
		return
	}
	full := filepath.Join(base, path)
	read := full
	if c.rules.SourceCopy != "" {
		if rel, err := filepath.Rel(c.rules.ComposeDir, full); err == nil && (rel == "src" || strings.HasPrefix(rel, "src/")) {
			read = filepath.Join(c.rules.SourceCopy, rel)
		}
	}
	content, err := os.ReadFile(read)
	if err != nil {
		return // config fails on a missing file anyway
	}
	if err := c.storedFile(string(content), filepath.Dir(full), depth+1); err != nil {
		c.add(orTop(service), "%s %s cannot be read", what, path)
	}
}

func (c *checker) storedFile(content, base string, depth int) error {
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
			c.follow("", "include", e, base, depth)
		case map[string]any:
			switch p := e["path"].(type) {
			case string:
				c.follow("", "include", p, base, depth)
			case []any:
				for _, item := range p {
					s, _ := item.(string)
					c.follow("", "include", s, base, depth)
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
				c.follow(name, "extends", file, base, depth)
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
