package sources

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"relist-panel/internal/volfs"
)

// ErrUnsafeZip refuses an entry that must not be written: an absolute name,
// one that climbs out, a symlink that leaves the tree, or an entry type other
// than a file, directory or symlink.
var ErrUnsafeZip = errors.New("unsafe zip entry")

const maxSymlinkTarget = 4096

type zipEntry struct {
	f    *zip.File
	name string // slash-separated, relative, root already stripped
	mode fs.FileMode
}

// ExtractZip unpacks zipPath into destDir, creating it, and returns how many
// files and symlinks it wrote. It follows the safety rules of volfs.Extract:
// every entry is checked before any is written, and writes go through an
// os.Root so nothing can land outside destDir. __MACOSX/ and .DS_Store are
// ignored, a single top-level directory is stripped, and executable bits are
// kept. Symlinks must be relative and resolve inside the tree; they are made
// last so no file is ever written through one. Exceeding limits wraps
// volfs.ErrArchiveTooLarge; a bad zip wraps volfs.ErrInvalidArchive.
func ExtractZip(zipPath, destDir string, limits volfs.ExtractLimits) (files int, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", volfs.ErrInvalidArchive, err)
	}
	defer zr.Close()

	if len(zr.File) > limits.MaxEntries {
		return 0, fmt.Errorf("%w: %d entries is past the %d-entry cap",
			volfs.ErrArchiveTooLarge, len(zr.File), limits.MaxEntries)
	}

	entries, err := checkZip(zr.File)
	if err != nil {
		return 0, err
	}

	var declared uint64
	for _, e := range entries {
		declared += e.f.UncompressedSize64
	}
	if declared > uint64(max(limits.MaxBytes, 0)) {
		return 0, fmt.Errorf("%w: %d bytes is past the %d-byte uncompressed cap",
			volfs.ErrArchiveTooLarge, declared, limits.MaxBytes)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(destDir)
	if err != nil {
		return 0, err
	}
	defer root.Close()

	remaining := limits.MaxBytes
	var symlinks []zipEntry
	for _, e := range entries {
		switch {
		case e.mode.IsDir():
			if err := root.MkdirAll(e.name, 0o755); err != nil {
				return files, err
			}
		case e.mode&fs.ModeSymlink != 0:
			symlinks = append(symlinks, e)
		default:
			n, err := writeZipFile(root, e, remaining)
			remaining -= n
			if err != nil {
				return files, err
			}
			files++
		}
	}
	for _, e := range symlinks {
		target, err := readSymlinkTarget(e.f)
		if err != nil {
			return files, err
		}
		if dir := path.Dir(e.name); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return files, err
			}
		}
		if err := root.Symlink(target, e.name); err != nil {
			return files, err
		}
		files++
	}
	return files, nil
}

// checkZip validates every entry and returns the ones to write, with junk
// dropped and a single root directory stripped.
func checkZip(all []*zip.File) ([]zipEntry, error) {
	var kept []*zip.File
	for _, f := range all {
		if isZipJunk(f.Name) {
			continue
		}
		kept = append(kept, f)
	}

	root := singleRoot(kept)
	var out []zipEntry
	for _, f := range kept {
		name, err := zipEntryName(f)
		if err != nil {
			return nil, err
		}
		if root != "" {
			if name == root {
				continue // the root directory itself
			}
			name = strings.TrimPrefix(name, root+"/")
		}
		mode := f.Mode()
		switch {
		case mode.IsDir(), mode.IsRegular():
		case mode&fs.ModeType == fs.ModeSymlink:
			target, err := readSymlinkTarget(f)
			if err != nil {
				return nil, err
			}
			if err := checkSymlink(name, target); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: entry %q is not a file, directory or symlink", ErrUnsafeZip, f.Name)
		}
		out = append(out, zipEntry{f: f, name: name, mode: mode})
	}
	return out, nil
}

func isZipJunk(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(strings.TrimSuffix(name, "/")) == ".DS_Store"
}

// singleRoot is the one top-level directory every entry sits under, or "".
func singleRoot(files []*zip.File) string {
	root := ""
	for _, f := range files {
		name := strings.TrimSuffix(f.Name, "/")
		first, _, nested := strings.Cut(name, "/")
		if !nested && !f.Mode().IsDir() {
			return "" // a file at the top level
		}
		if root == "" {
			root = first
		} else if root != first {
			return ""
		}
	}
	return root
}

// zipEntryName judges a name as it stands, like volfs's entryName: relative,
// slash-separated, with no `..` or empty segment under either separator.
func zipEntryName(f *zip.File) (string, error) {
	name := f.Name
	if f.Mode().IsDir() {
		name = strings.TrimSuffix(name, "/")
	}
	switch {
	case name == "":
		return "", fmt.Errorf("%w: entry has an empty name", ErrUnsafeZip)
	case path.IsAbs(name) || name[0] == '\\' || len(name) >= 2 && name[1] == ':':
		return "", fmt.Errorf("%w: entry %q is absolute", ErrUnsafeZip, f.Name)
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("%w: entry %q has a %q segment", ErrUnsafeZip, f.Name, "..")
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			return "", fmt.Errorf("%w: entry %q has an empty segment", ErrUnsafeZip, f.Name)
		}
	}
	return name, nil
}

func readSymlinkTarget(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("%w: %w", volfs.ErrInvalidArchive, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxSymlinkTarget+1))
	if err != nil {
		return "", fmt.Errorf("%w: %w", volfs.ErrInvalidArchive, err)
	}
	if len(b) > maxSymlinkTarget {
		return "", fmt.Errorf("%w: symlink %q has a target over %d bytes", ErrUnsafeZip, f.Name, maxSymlinkTarget)
	}
	return string(b), nil
}

// checkSymlink requires a relative target that, resolved from the link's own
// directory, stays under the tree.
func checkSymlink(name, target string) error {
	if target == "" || path.IsAbs(target) || strings.ContainsRune(target, '\\') {
		return fmt.Errorf("%w: symlink %q has target %q", ErrUnsafeZip, name, target)
	}
	resolved := path.Join(path.Dir(name), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%w: symlink %q points outside the tree (%q)", ErrUnsafeZip, name, target)
	}
	return nil
}

func writeZipFile(root *os.Root, e zipEntry, remaining int64) (int64, error) {
	if dir := path.Dir(e.name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	rc, err := e.f.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", volfs.ErrInvalidArchive, err)
	}
	defer rc.Close()
	out, err := root.OpenFile(e.name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	if remaining < 0 {
		remaining = 0
	}
	// One byte past the budget tells an overflow from an exact fit.
	n, err := io.Copy(out, io.LimitReader(rc, remaining+1))
	if err == nil && n > remaining {
		err = fmt.Errorf("%w: entry %q is past the uncompressed cap", volfs.ErrArchiveTooLarge, e.f.Name)
	} else if err != nil {
		err = fmt.Errorf("%w: %w", volfs.ErrInvalidArchive, err)
	}
	if err == nil {
		// Keep only the permission bits, never setuid, setgid or sticky.
		err = out.Chmod(e.mode.Perm() | 0o200)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}
