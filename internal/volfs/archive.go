package volfs

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"path"
)

// Archive streams name into dst as a gzip-compressed tar, the one archive
// format this feature speaks in both directions. dst need not be seekable —
// the intended destination is an HTTP response body — and nothing is staged
// on disk or in memory: List feeds the walk and Read feeds file bytes straight
// into the tar writer.
//
// Entry names are set explicitly to the path relative to the Volume root,
// slash-separated, never absolute and never carrying a `..` segment.
// tar.FileInfoHeader fills in only an entry's base name, so no header is
// derived from it here: every field is built from the Entry. A named directory
// gets its own entry, so an empty one survives a round trip; the Volume root
// itself is the exception, because archiving it means archiving its contents,
// and extraction's target directory already exists.
//
// A symlink is stored as a symlink with its target, faithfully — including one
// that points outside the root, because the archive records what the Volume
// holds and the hazard belongs to extraction, where such an entry is refused.
// A hardlink is a regular file on Unix and streams as one, never as a hardlink
// entry, which sidesteps the hardlink tar-slip vector entirely. A device node,
// socket or FIFO cannot be streamed without risking a block and has no meaning
// in an archive that is restored file by file, so KindOther is skipped.
//
// Any failure — a refused read or a failed write, as when the caller's HTTP
// request is cancelled — aborts the walk and is returned.
func (fs *FS) Archive(name string, dst io.Writer) error {
	zw := gzip.NewWriter(dst)
	tw := tar.NewWriter(zw)

	// Clean the caller's path so an entry name can never carry a `..` segment
	// or a leading slash even when the input does. Stat still resolves the
	// real path through the root; cleaning only shapes the names.
	if err := fs.archiveEntry(tw, path.Clean(name)); err != nil {
		return fmt.Errorf("archive %q: %w", name, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("archive %q: %w", name, err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("archive %q: %w", name, err)
	}
	return nil
}

// archiveEntry writes one path and, for a directory, recurses into its
// children with List. The path is volume-relative, and the entry name is that
// path as it stands, so it can never be absolute or climb out of the root.
func (fs *FS) archiveEntry(tw *tar.Writer, name string) error {
	entry, err := fs.Stat(name)
	if err != nil {
		return err
	}

	switch entry.Kind {
	case KindDir:
		return fs.archiveDir(tw, name, entry)
	case KindFile:
		if err := tw.WriteHeader(fileHeader(name, entry)); err != nil {
			return err
		}
		return fs.Read(name, tw)
	case KindSymlink:
		return tw.WriteHeader(symlinkHeader(name, entry))
	default:
		// KindOther: a device, socket or FIFO. Opening a FIFO for reading
		// blocks until a writer appears, so streaming one is not safe, and
		// none of them has a representation a restore could re-create.
		return nil
	}
}

// archiveDir writes a directory's own entry and then its children. The root
// itself gets no entry — its children are already named relative to it, and
// extraction's target directory must exist anyway.
func (fs *FS) archiveDir(tw *tar.Writer, name string, entry Entry) error {
	if name != "." {
		if err := tw.WriteHeader(dirHeader(name, entry)); err != nil {
			return err
		}
	}

	children, err := fs.List(name)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := fs.archiveEntry(tw, path.Join(name, child.Name)); err != nil {
			return err
		}
	}
	return nil
}

// tarHeader carries the metadata Stat and List already read into a header;
// the caller sets the Typeflag and any type-specific fields, and the name is
// always this path — never a base name.
func tarHeader(name string, entry Entry) *tar.Header {
	return &tar.Header{
		Name:    name,
		Mode:    int64(entry.Mode.Perm()),
		Uid:     int(entry.UID),
		Gid:     int(entry.GID),
		ModTime: entry.ModTime,
	}
}

// fileHeader describes one regular file. A hardlink is not a distinct shape
// here: Stat reports it as a regular file, and Read streams its bytes.
func fileHeader(name string, entry Entry) *tar.Header {
	h := tarHeader(name, entry)
	h.Typeflag = tar.TypeReg
	h.Size = entry.Size
	return h
}

// dirHeader describes one directory, with the trailing slash tar's convention
// gives directory names.
func dirHeader(name string, entry Entry) *tar.Header {
	h := tarHeader(name+"/", entry)
	h.Typeflag = tar.TypeDir
	return h
}

// symlinkHeader describes one symlink. The target is recorded verbatim —
// absolute or relative, inside the root or out — and Size stays zero, which
// tar requires of a non-regular entry.
func symlinkHeader(name string, entry Entry) *tar.Header {
	h := tarHeader(name, entry)
	h.Typeflag = tar.TypeSymlink
	h.Linkname = entry.Link
	return h
}
