package sources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	if err := os.WriteFile(filepath.Join(newDir, MarkerName), nil, 0o644); err != nil {
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
