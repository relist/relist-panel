package volfs

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

// ExtractLimits are one extraction's caps: how many entries a tar.gz may
// carry and how many uncompressed bytes it may write. Zero means zero, so a
// caller that wants the spec's limits asks for DefaultExtractLimits; Extract
// itself uses them when no limits are given.
type ExtractLimits struct {
	// MaxEntries is the most entries the stream may carry.
	MaxEntries int
	// MaxBytes is the most uncompressed bytes it may write.
	MaxBytes int64
}

// DefaultExtractLimits is the spec's restore caps: 10,000 entries and 4 GiB
// uncompressed. The 1 GiB compressed cap belongs to the HTTP layer, on the
// request body, and is not visible to this stream.
func DefaultExtractLimits() ExtractLimits {
	return ExtractLimits{MaxEntries: 10_000, MaxBytes: 4 << 30}
}

// The two refusals an extraction can make, kept apart from an ordinary
// failure so the helper can map each to its own exit code. The offending
// entry is wrapped around the sentinel, never baked into it, so errors.Is
// matches every refusal of a class.
var (
	// ErrUnsafeArchive refuses an entry that must not be written: a name
	// that is absolute or climbs out, or a type that is not a regular file
	// or directory.
	ErrUnsafeArchive = errors.New("unsafe archive entry")
	// ErrArchiveTooLarge refuses a stream that runs past a restore cap.
	ErrArchiveTooLarge = errors.New("archive exceeds the restore limits")
)

// Extract streams a tar.gz from src into target, a directory relative to the
// Volume root that must already exist: a missing or non-directory target is
// an error, never a create. Nothing is staged — gzip and tar decompress as
// bytes arrive, each entry is written as soon as its header is validated, and
// the stream is never held whole in memory or on disk.
//
// Every header is validated before anything is written for it: the name must
// be relative, slash-separated and free of empty and `..` segments, and never
// a Windows drive or UNC shape; only regular files and directories are
// accepted, so a symlink, hardlink, FIFO, device or any other entry refuses
// the stream. Directories are created and merged through the root, files are
// created and overwritten through the root, a header's permission bits are
// applied, and nothing else is ever deleted.
//
// A refusal aborts the rest of the stream and wraps the offending entry
// around ErrUnsafeArchive or ErrArchiveTooLarge; an ordinary failure — a
// malformed gzip, a truncated entry, a failed write — is returned as itself.
//
// There is no transaction across a stream: entries already written when a
// later header is refused, or when the stream dies, stay written, and a file
// whose body fails mid-copy stays truncated. Directories stay owner-writable
// until the whole stream has been read, so a directory whose header mode
// forbids its own children does not lock them out; its exact mode is applied
// once the stream ends.
//
// Callers may pass at most one ExtractLimits to inject caps; with none, the
// spec's defaults are used.
func (fs *FS) Extract(target string, src io.Reader, limits ...ExtractLimits) error {
	lim := DefaultExtractLimits()
	switch len(limits) {
	case 0:
	case 1:
		lim = limits[0]
	default:
		return fmt.Errorf("extract %q: at most one ExtractLimits, got %d", target, len(limits))
	}

	// The target is checked before a byte of src is read, so a caller can
	// learn that it must exist before the stream is consumed.
	info, err := fs.root.Stat(target)
	if err != nil {
		return fmt.Errorf("extract %q: %w", target, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("extract %q: target is not a directory", target)
	}

	zr, err := gzip.NewReader(src)
	if err != nil {
		if errors.Is(err, io.EOF) {
			// An empty stream is a truncated one, not a clean end.
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("extract %q: open gzip: %w", target, err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	var (
		entries int
		written int64
		dirs    []dirMode
	)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("extract %q: %w", target, err)
		}

		entries++
		if entries > lim.MaxEntries {
			return fmt.Errorf("extract %q: %w: entry %d (%q) is past the %d-entry cap",
				target, ErrArchiveTooLarge, entries, hdr.Name, lim.MaxEntries)
		}

		name, err := entryName(target, hdr)
		if err != nil {
			return fmt.Errorf("extract %q: %w", target, err)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			n, err := fs.extractFile(name, tr, headerPerm(hdr), lim.MaxBytes-written)
			written += n
			if errors.Is(err, errCapReached) {
				return fmt.Errorf("extract %q: %w: entry %q is past the %d-byte uncompressed cap",
					target, ErrArchiveTooLarge, hdr.Name, lim.MaxBytes)
			}
			if err != nil {
				return fmt.Errorf("extract %q: %w", target, err)
			}
		case tar.TypeDir:
			// Owner-writable for now; the exact bits are applied once every
			// child exists.
			if err := fs.root.MkdirAll(name, headerPerm(hdr)|0o700); err != nil {
				return fmt.Errorf("extract %q: %w", target, err)
			}
			dirs = append(dirs, dirMode{name: name, mode: headerPerm(hdr)})
		default:
			return fmt.Errorf("extract %q: %w: entry %q is %s",
				target, ErrUnsafeArchive, hdr.Name, entryType(hdr))
		}
	}

	// Deepest first: a directory takes its exact mode only after everything
	// beneath it has been written.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := fs.root.Chmod(dirs[i].name, dirs[i].mode); err != nil {
			return fmt.Errorf("extract %q: chmod %q: %w", target, dirs[i].name, err)
		}
	}
	return nil
}

// dirMode is one directory's header mode, remembered until the stream has
// been read: the directory itself is created writable first, because a mode
// like 0555 would otherwise refuse the entry's own children.
type dirMode struct {
	name string
	mode fs.FileMode
}

// headerPerm is a header's permission bits and nothing above them: setuid,
// setgid and the sticky bit are never restored.
func headerPerm(hdr *tar.Header) fs.FileMode {
	return fs.FileMode(hdr.Mode).Perm()
}

// extractFile writes one regular file, created or truncated through the root
// and streamed from the tar reader. remaining is the bytes left in the
// uncompressed budget; the file is left as it stands if any step fails,
// because a stream has no transaction to roll back.
func (fs *FS) extractFile(name string, body io.Reader, perm fs.FileMode, remaining int64) (int64, error) {
	f, err := fs.root.Create(name)
	if err != nil {
		return 0, err
	}

	n, err := copyWithin(f, body, remaining)
	if err != nil {
		f.Close()
		return n, err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return n, err
	}
	if err := f.Close(); err != nil {
		return n, err
	}
	return n, nil
}

// errCapReached says a body still had bytes when the uncompressed budget ran
// out. Extract turns it into ErrArchiveTooLarge, so it stays distinct from an
// ordinary read failure.
var errCapReached = errors.New("uncompressed cap reached")

// copyWithin streams a body into dst, writing at most remaining bytes, and
// reports how many it wrote. A body that ends exactly at the budget is not an
// overflow; one with a byte to spare past it is.
func copyWithin(dst io.Writer, body io.Reader, remaining int64) (int64, error) {
	if remaining < 0 {
		remaining = 0
	}
	n, err := io.Copy(dst, io.LimitReader(body, remaining))
	if err != nil {
		return n, err
	}
	if n < remaining {
		return n, nil
	}

	// The budget is spent. Probe one byte: io.EOF means the body fit exactly,
	// anything else means the entry is past the cap.
	var probe [1]byte
	switch m, err := body.Read(probe[:]); {
	case m > 0:
		return n, errCapReached
	case err != nil && !errors.Is(err, io.EOF):
		return n, err
	default:
		return n, nil
	}
}

// entryName validates one header's name and returns the path to write,
// relative to the Volume root. The name is judged as it stands rather than
// cleaned first, so `a/../b` and `a//b` — which would clean to something
// harmless — are refused instead of silently rewritten.
//
// A directory's one trailing slash is tar's convention, not an empty segment.
// Everything else must be relative and slash-separated, never absolute and
// never a Windows drive or UNC shape, with no `..` segment under either
// separator, so a `..\..\escape` cannot be read as a climb by a platform that
// treats the backslash as one.
func entryName(target string, hdr *tar.Header) (string, error) {
	name := hdr.Name
	if hdr.Typeflag == tar.TypeDir {
		name = strings.TrimSuffix(name, "/")
	}

	switch {
	case name == "":
		return "", fmt.Errorf("%w: entry has an empty name", ErrUnsafeArchive)
	case path.IsAbs(name) || isWindowsAbs(name):
		return "", fmt.Errorf("%w: entry %q is absolute", ErrUnsafeArchive, hdr.Name)
	}
	for _, segment := range strings.FieldsFunc(name, isPathSeparator) {
		if segment == ".." {
			return "", fmt.Errorf("%w: entry %q has a %q segment", ErrUnsafeArchive, hdr.Name, "..")
		}
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" {
			return "", fmt.Errorf("%w: entry %q has an empty segment", ErrUnsafeArchive, hdr.Name)
		}
	}
	return path.Join(target, name), nil
}

// isPathSeparator covers both separators a name must survive: the slash this
// stream speaks, and the backslash another platform would read as one.
func isPathSeparator(r rune) bool { return r == '/' || r == '\\' }

// isWindowsAbs reports whether name is a Windows drive or UNC path in
// disguise: `C:\x`, `C:foo`, `\\host\share` or a rooted `\x`. The helper runs
// on Linux, where those are ordinary bytes, but a name must never be readable
// as absolute anywhere.
func isWindowsAbs(name string) bool {
	if name[0] == '\\' {
		return true
	}
	return len(name) >= 2 && name[1] == ':' && isASCIILetter(name[0])
}

func isASCIILetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// entryType names an entry's type for a refusal: the unsafe shapes get their
// own words, and anything else is reported by its raw typeflag.
func entryType(hdr *tar.Header) string {
	switch hdr.Typeflag {
	case tar.TypeSymlink:
		return "a symlink"
	case tar.TypeLink:
		return "a hardlink"
	case tar.TypeFifo:
		return "a FIFO"
	case tar.TypeChar:
		return "a character device"
	case tar.TypeBlock:
		return "a block device"
	default:
		return fmt.Sprintf("type %#x", hdr.Typeflag)
	}
}
