package accesslog

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// liveName is the file Traefik writes; every other file is rotated.
const liveName = "access.log"

// namePattern matches the live file and every rotated one, plain or gzipped.
// Nothing else in the directory is listed, served or touched.
var namePattern = regexp.MustCompile(`^access(-\d{4}-\d{2}-\d{2}(-\d+)?)?\.log(\.gz)?$`)

// rotatedPattern captures a rotated file's date and whether it is gzipped.
var rotatedPattern = regexp.MustCompile(`^access-(\d{4}-\d{2}-\d{2})(-\d+)?\.log(\.gz)?$`)

// Dir is the Access log directory, which Traefik sees as /letsencrypt/access.
func Dir(dataDir string) string { return filepath.Join(dataDir, ".traefik", "access") }

// ValidName reports whether name is the live file or a rotated one.
func ValidName(name string) bool {
	return namePattern.MatchString(name) && name != liveName+".gz"
}

type File struct {
	Name string
	Date string // YYYY-MM-DD; "" for the live file
	Size int64
}

// List returns the live file first, then rotated files newest first, and
// their total size. A missing directory is empty.
func List(dir string) ([]File, int64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []File{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	files := []File{}
	var total int64
	for _, e := range entries {
		if !e.Type().IsRegular() || !ValidName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := File{Name: e.Name(), Size: info.Size()}
		if m := rotatedPattern.FindStringSubmatch(e.Name()); m != nil {
			f.Date = m[1]
		}
		files = append(files, f)
		total += f.Size
	}
	sort.Slice(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if (a.Date == "") != (b.Date == "") {
			return a.Date == ""
		}
		if a.Date != b.Date {
			return a.Date > b.Date
		}
		return a.Name < b.Name
	})
	return files, total, nil
}
