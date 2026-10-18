package volfs

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// Pack streams name into dst as an uncompressed tar for a server Migration
// (ADR-0018). Unlike Archive it keeps everything a restored Volume needs to
// run: numeric owners, the full mode including setuid, setgid and sticky,
// mtimes, symlinks as links and empty directories. Entry names are relative
// to name's parent, so packing one file yields one entry and packing "."
// yields the root's children. Devices, sockets and FIFOs are skipped.
func (fs *FS) Pack(name string, dst io.Writer) error {
	return fs.PackExcluding(name, nil, dst)
}

// PackExcluding is Pack with skip, which is given each path relative to the
// root and leaves out every path it reports true for, with its descendants.
func (fs *FS) PackExcluding(name string, skip func(rel string) bool, dst io.Writer) error {
	name = path.Clean(name)
	tw := tar.NewWriter(dst)
	prefix := ""
	if dir := path.Dir(name); name != "." && dir != "." {
		prefix = dir + "/"
	}
	p := packer{fs: fs, tw: tw, prefix: prefix, skip: skip}
	if err := p.entry(name); err != nil {
		return fmt.Errorf("pack %q: %w", name, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("pack %q: %w", name, err)
	}
	return nil
}

type packer struct {
	fs     *FS
	tw     *tar.Writer
	prefix string
	skip   func(string) bool
}

func (p packer) entry(name string) error {
	if name != "." && p.skip != nil && p.skip(name) {
		return nil
	}
	entry, err := p.fs.Stat(name)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    strings.TrimPrefix(name, p.prefix),
		Mode:    tarMode(entry.Mode),
		Uid:     int(entry.UID),
		Gid:     int(entry.GID),
		ModTime: entry.ModTime,
		Format:  tar.FormatPAX,
	}
	switch entry.Kind {
	case KindDir:
		if name != "." {
			hdr.Name += "/"
			hdr.Typeflag = tar.TypeDir
			if err := p.tw.WriteHeader(hdr); err != nil {
				return err
			}
		}
		children, err := p.fs.List(name)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := p.entry(path.Join(name, child.Name)); err != nil {
				return err
			}
		}
		return nil
	case KindFile:
		hdr.Typeflag = tar.TypeReg
		hdr.Size = entry.Size
		if err := p.tw.WriteHeader(hdr); err != nil {
			return err
		}
		return p.fs.Read(name, p.tw)
	case KindSymlink:
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = entry.Link
		return p.tw.WriteHeader(hdr)
	default:
		return nil
	}
}

// tarMode is a file mode in tar's bits, special bits included.
func tarMode(m fs.FileMode) int64 {
	mode := int64(m.Perm())
	if m&fs.ModeSetuid != 0 {
		mode |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		mode |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		mode |= 0o1000
	}
	return mode
}

// fileMode is tarMode's inverse.
func fileMode(mode int64) fs.FileMode {
	m := fs.FileMode(mode).Perm()
	if mode&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if mode&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if mode&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

// Unpack extracts a Pack stream into the root, with no entry or byte cap,
// restoring owners, modes and mtimes. Names that are absolute or climb out
// are refused, and so is any entry beneath a symlink, so nothing is ever
// written through a link. Symlinks themselves are restored with their target
// verbatim. Hardlinks, devices and FIFOs are refused. Every refusal wraps
// ErrUnsafeArchive; a malformed stream wraps ErrInvalidArchive.
func (fs *FS) Unpack(src io.Reader) error {
	tr := tar.NewReader(src)
	type dirMeta struct {
		name string
		hdr  *tar.Header
	}
	var dirs []dirMeta
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("unpack: %w: %w", ErrInvalidArchive, err)
		}
		name, err := entryName(".", hdr)
		if err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		if err := fs.refuseLinkedParent(name); err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := fs.root.MkdirAll(name, 0o700); err != nil {
				return fmt.Errorf("unpack: %w", err)
			}
			dirs = append(dirs, dirMeta{name: name, hdr: hdr})
		case tar.TypeReg, tar.TypeRegA:
			if err := fs.unpackFile(name, hdr, tr); err != nil {
				return fmt.Errorf("unpack: %w", err)
			}
		case tar.TypeSymlink:
			if err := fs.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("unpack: %w", err)
			}
			if err := fs.root.Symlink(hdr.Linkname, name); err != nil {
				return fmt.Errorf("unpack: %w", err)
			}
			if err := fs.root.Lchown(name, hdr.Uid, hdr.Gid); err != nil {
				return fmt.Errorf("unpack: %w", err)
			}
		default:
			return fmt.Errorf("unpack: %w: entry %q is %s", ErrUnsafeArchive, hdr.Name, entryType(hdr))
		}
	}
	// Deepest first, after every child exists: the owner, then the mode,
	// because chown clears setuid and setgid, then the mtime the writes moved.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := fs.applyMeta(dirs[i].name, dirs[i].hdr); err != nil {
			return fmt.Errorf("unpack: %w", err)
		}
	}
	return nil
}

func (fs *FS) unpackFile(name string, hdr *tar.Header, body io.Reader) error {
	if info, err := fs.root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		if err := fs.root.RemoveAll(name); err != nil {
			return err
		}
	}
	f, err := fs.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return fmt.Errorf("%w: entry %q: %w", ErrInvalidArchive, hdr.Name, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fs.applyMeta(name, hdr)
}

func (fs *FS) applyMeta(name string, hdr *tar.Header) error {
	if err := fs.root.Lchown(name, hdr.Uid, hdr.Gid); err != nil {
		return err
	}
	if err := fs.root.Chmod(name, fileMode(hdr.Mode)); err != nil {
		return err
	}
	return fs.root.Chtimes(name, time.Time{}, hdr.ModTime)
}

// refuseLinkedParent refuses name when any directory above it is a symlink.
func (fs *FS) refuseLinkedParent(name string) error {
	dir := path.Dir(name)
	for dir != "." && dir != "/" {
		info, err := fs.root.Lstat(dir)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: entry %q is beneath the symlink %q", ErrUnsafeArchive, name, dir)
		}
		dir = path.Dir(dir)
	}
	return nil
}

// Size is the sum of the regular file sizes under name. Symlinks are not
// followed.
func (fs *FS) Size(name string) (int64, error) {
	entry, err := fs.Stat(name)
	if err != nil {
		return 0, err
	}
	switch entry.Kind {
	case KindFile:
		return entry.Size, nil
	case KindDir:
		children, err := fs.List(name)
		if err != nil {
			return 0, err
		}
		var total int64
		for _, child := range children {
			n, err := fs.Size(path.Join(name, child.Name))
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	default:
		return 0, nil
	}
}
