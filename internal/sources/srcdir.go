package sources

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// MarkerName is the file that says a Compose's src/ belongs to its Source.
const MarkerName = ".relist-source"

// ErrForeignSrc refuses to touch a src/ the panel did not create.
var ErrForeignSrc = errors.New("src/ already exists in this Compose's directory, move it first")

// EnsureOwnedSrc is nil when the Compose directory has no src/ or its src/
// carries the marker, and ErrForeignSrc otherwise.
func EnsureOwnedSrc(composeDir string) error {
	src := filepath.Join(composeDir, "src")
	info, err := os.Lstat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect src: %w", err)
	}
	if !info.IsDir() {
		return ErrForeignSrc
	}
	if _, err := os.Lstat(filepath.Join(src, MarkerName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrForeignSrc
		}
		return fmt.Errorf("inspect src marker: %w", err)
	}
	return nil
}

// ReplaceSrc makes newDir the Compose's src/. It writes the marker into
// newDir, renames any existing src/ aside, renames newDir to src, then
// removes the old code. If the second rename fails the old src/ is put back,
// so a failure leaves the previous code in place. Callers check ownership
// with EnsureOwnedSrc first, and newDir must be on the same filesystem.
func ReplaceSrc(composeDir, newDir string) error {
	src := filepath.Join(composeDir, "src")
	old := filepath.Join(composeDir, ".src.old")

	// The new tree is untrusted: a link named like the marker must not be
	// followed, so drop whatever is there and create the marker exclusively.
	marker := filepath.Join(newDir, MarkerName)
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mark new src: %w", err)
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("mark new src: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("mark new src: %w", err)
	}
	// A leftover from a crashed swap is the panel's own, safe to drop.
	if err := os.RemoveAll(old); err != nil {
		return fmt.Errorf("clear %s: %w", old, err)
	}
	hadOld := true
	if err := os.Rename(src, old); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move old src aside: %w", err)
		}
		hadOld = false
	}
	if err := os.Rename(newDir, src); err != nil {
		if hadOld {
			if rerr := os.Rename(old, src); rerr != nil {
				err = fmt.Errorf("%w (restoring the old src also failed: %v)", err, rerr)
			}
		}
		return fmt.Errorf("install new src: %w", err)
	}
	if hadOld {
		if err := os.RemoveAll(old); err != nil {
			return fmt.Errorf("remove old src: %w", err)
		}
	}
	return nil
}

// CleanStaging clears what a crash mid-upload leaves in a Compose directory:
// .upload-*.zip, .src.new-* and .src.old. A missing src/ with a .src.old means
// ReplaceSrc died between its two renames, and the database still describes
// that old tree, so it is put back before anything is removed. With neither a
// src/ nor a .src.old, a .src.new-* may be the only copy and stays. Matches
// are direct children only, and a link is removed as a link.
func CleanStaging(composeDir string) error {
	entries, err := os.ReadDir(composeDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list %s: %w", composeDir, err)
	}
	var uploads, staged []string
	hasOld := false
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, ".upload-") && strings.HasSuffix(name, ".zip"):
			uploads = append(uploads, name)
		case strings.HasPrefix(name, ".src.new-"):
			staged = append(staged, name)
		case name == ".src.old":
			hasOld = true
		}
	}

	var errs []error
	remove := func(name string) {
		path := filepath.Join(composeDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return
		}
		if info.IsDir() {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	for _, name := range uploads {
		remove(name)
	}

	src := filepath.Join(composeDir, "src")
	old := filepath.Join(composeDir, ".src.old")
	srcInfo, srcErr := os.Lstat(src)
	switch {
	case srcErr == nil && srcInfo.IsDir():
	case errors.Is(srcErr, os.ErrNotExist):
		oldInfo, err := os.Lstat(old)
		if !hasOld || err != nil || !oldInfo.IsDir() {
			if len(staged) > 0 {
				log.Printf("sources: %s has no src/ and no .src.old but keeps %d staged tree(s); left in place", composeDir, len(staged))
			}
			if hasOld {
				remove(".src.old")
			}
			return errors.Join(errs...)
		}
		if err := os.Rename(old, src); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", old, err))
			return errors.Join(errs...)
		}
		log.Printf("sources: restored %s from .src.old after an interrupted upload", src)
		hasOld = false
	default:
		// src/ is not a directory the panel can reason about; touch nothing else.
		return errors.Join(errs...)
	}

	for _, name := range staged {
		remove(name)
	}
	if hasOld {
		remove(".src.old")
	}
	return errors.Join(errs...)
}
