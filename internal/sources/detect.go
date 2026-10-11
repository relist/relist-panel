package sources

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"relist-panel/internal/slug"
)

const maxDetectDepth = 4

// Dockerfile is one Dockerfile found in a Source, with the Service name and
// build context the editor would suggest for it.
type Dockerfile struct {
	Path       string // relative to src, slash-separated
	Service    string
	Context    string // like "./src/api"
	Dockerfile string // file name, for build.dockerfile
}

// DetectDockerfiles walks srcDir to depth 4, skipping .git, node_modules and
// vendor, and returns every Dockerfile, Dockerfile.<x> and <x>.Dockerfile in
// path order. A missing srcDir has none. composeName names the Service for a
// Dockerfile at the root; duplicate names get -2, -3.
func DetectDockerfiles(srcDir, composeName string) ([]Dockerfile, error) {
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil, nil
	}
	var out []Dockerfile
	used := map[string]bool{}
	err := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch {
			case rel == ".":
			case d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor":
				return filepath.SkipDir
			case len(strings.Split(rel, string(filepath.Separator))) > maxDetectDepth:
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		base := suggestedBase(d.Name())
		if base == "" {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		name := base
		if name == "\x00" { // plain "Dockerfile": named after its directory
			name = composeName
			if dir != "." {
				name = filepath.Base(dir)
			}
		}
		ctx := "./src"
		if dir != "." {
			ctx += "/" + dir
		}
		out = append(out, Dockerfile{
			Path:       filepath.ToSlash(rel),
			Service:    uniqueService(serviceName(name), used),
			Context:    ctx,
			Dockerfile: d.Name(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("detect Dockerfiles: %w", err)
	}
	return out, nil
}

// suggestedBase is "" for a file that is not a Dockerfile, "\x00" for a plain
// Dockerfile, and otherwise the <x> of Dockerfile.<x> or <x>.Dockerfile.
func suggestedBase(file string) string {
	switch {
	case file == "Dockerfile":
		return "\x00"
	case strings.HasPrefix(file, "Dockerfile.") && len(file) > len("Dockerfile."):
		return strings.TrimPrefix(file, "Dockerfile.")
	case strings.HasSuffix(file, ".Dockerfile") && len(file) > len(".Dockerfile"):
		return strings.TrimSuffix(file, ".Dockerfile")
	}
	return ""
}

// serviceName makes a Compose Service name: the slug, restricted to [a-z0-9_-].
func serviceName(v string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, slug.Slugify(v))
}

func uniqueService(name string, used map[string]bool) string {
	candidate := name
	for n := 2; used[candidate]; n++ {
		candidate = name + "-" + strconv.Itoa(n)
	}
	used[candidate] = true
	return candidate
}
