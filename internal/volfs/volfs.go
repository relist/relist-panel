// Package volfs is the path-safe filesystem layer beneath a Volume's mount:
// every operation runs against one os.OpenRoot, so containment is enforced by
// the root itself — never by string checks — and there is no
// validate-then-use gap to race (ADR-0010, the spec's Names, symlinks,
// hardlinks table).
//
// Paths are always relative to the Volume root and are handed to os.Root
// unchanged. `..`, absolute paths and `dir/../../x` all fail with os.Root's
// own "path escapes from parent"; a symlink that stays inside the root is
// followed, and one that points outside is listed but refused on open.
// Production opens the helper's mount at /data; tests open a t.TempDir().
//
// Bytes are streamed: Read and Write never hold a whole file in memory, and
// nothing is staged on disk. The tar.gz writer and the helper dispatch onto
// these calls, and reuse List's metadata to describe what an archive holds.
package volfs

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"
)

// FS is one opened Volume root. Every method takes a path relative to it, and
// every operation resolves through the root, so nothing outside the Volume is
// ever reachable.
type FS struct {
	root *os.Root
}

// Open opens rootDir as a Volume root. Production passes the helper's mount,
// /data; tests pass a t.TempDir().
func Open(rootDir string) (*FS, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, fmt.Errorf("open volume root: %w", err)
	}
	return &FS{root: root}, nil
}

// Close releases the root's handle; no operation is valid afterwards.
func (fs *FS) Close() error {
	return fs.root.Close()
}

// Kind is what an entry is, derived from the metadata call's own mode bits.
type Kind string

const (
	KindFile    Kind = "file"
	KindDir     Kind = "directory"
	KindSymlink Kind = "symlink"
	// KindOther covers everything else a Volume can hold — a device, socket
	// or FIFO — reported honestly rather than mislabelled as one of the three.
	KindOther Kind = "other"
)

// Entry is one path's metadata: the same shape for List's children and for
// Stat. UID and GID are the owning account's, from the platform's stat
// structure. Link carries a symlink's target as reported by Readlink — for
// every symlink, including one that points outside the root, so the Explorer
// can describe what it will refuse to follow.
type Entry struct {
	Name    string      `json:"name"`
	Kind    Kind        `json:"kind"`
	Size    int64       `json:"size"`
	Mode    fs.FileMode `json:"mode"`
	ModTime time.Time   `json:"mtime"`
	UID     uint32      `json:"uid"`
	GID     uint32      `json:"gid"`
	Link    string      `json:"link,omitempty"`
}

// List returns one directory level's entries, sorted by name. Raw names come
// from the directory handle's ReadDir, never from parsing a rendered listing,
// so a name containing a newline round-trips intact. Each entry carries Lstat
// metadata, and a symlink's target comes from Readlink: described here,
// followed only by Read, and then only if it stays inside the root.
func (fs *FS) List(name string) ([]Entry, error) {
	dir, err := fs.root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", name, err)
	}
	defer dir.Close()

	items, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", name, err)
	}

	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		entry, err := fs.entry(path.Join(name, item.Name()))
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", name, err)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// Stat describes one path exactly as a List entry would: the entry itself,
// never following a final symlink, so a link that points outside the root is
// still describable while Read and Write on it are refused.
func (fs *FS) Stat(name string) (Entry, error) {
	entry, err := fs.entry(name)
	if err != nil {
		return Entry{}, fmt.Errorf("stat %q: %w", name, err)
	}
	return entry, nil
}

// entry reads one path's Lstat metadata through the root; a symlink also gets
// its Readlink target. Both calls resolve every component inside the root.
func (fs *FS) entry(name string) (Entry, error) {
	info, err := fs.root.Lstat(name)
	if err != nil {
		return Entry{}, err
	}
	uid, gid := owner(info)
	entry := Entry{
		Name:    info.Name(),
		Kind:    kindOf(info.Mode()),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		UID:     uid,
		GID:     gid,
	}
	if entry.Kind == KindSymlink {
		target, err := fs.root.Readlink(name)
		if err != nil {
			return Entry{}, err
		}
		entry.Link = target
	}
	return entry, nil
}

// kindOf names the entry's type from the metadata's mode bits. The symlink
// check precedes the regular-file one, so a link is never reported as its
// target.
func kindOf(mode fs.FileMode) Kind {
	switch {
	case mode.IsDir():
		return KindDir
	case mode&fs.ModeSymlink != 0:
		return KindSymlink
	case mode.IsRegular():
		return KindFile
	default:
		return KindOther
	}
}

// owner reads the owning uid/gid from the platform's stat structure. The
// helper runs on Linux, and a zero pair is the honest answer wherever the
// structure is absent.
func owner(info fs.FileInfo) (uid, gid uint32) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Uid, st.Gid
	}
	return 0, 0
}

// Read streams one file's bytes into dst. A symlink is followed only if the
// root can resolve it — one that points outside fails with os.Root's "path
// escapes from parent" — and a directory is refused by the kernel.
func (fs *FS) Read(name string, dst io.Writer) error {
	f, err := fs.root.Open(name)
	if err != nil {
		return fmt.Errorf("read %q: %w", name, err)
	}
	defer f.Close()

	if _, err := io.Copy(dst, f); err != nil {
		return fmt.Errorf("read %q: %w", name, err)
	}
	return nil
}

// Write streams src into one file, creating it or truncating it first, and
// never buffers the stream here. A symlink out of the root is refused exactly
// as Read refuses it: Create resolves the final component through the root
// too.
func (fs *FS) Write(name string, src io.Reader) error {
	f, err := fs.root.Create(name)
	if err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}

	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return fmt.Errorf("write %q: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	return nil
}

// Delete removes one path: a file, a symlink, or a whole directory tree. The
// root itself is never deletable — os.Root refuses — and a path that does not
// exist is a no-op, both matching RemoveAll.
func (fs *FS) Delete(name string) error {
	if err := fs.root.RemoveAll(name); err != nil {
		return fmt.Errorf("delete %q: %w", name, err)
	}
	return nil
}
