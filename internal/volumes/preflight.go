package volumes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PreflightStatus is one Volume's pre-flight outcome. The API layer renders
// each: PreflightOK lets the operation run, PreflightMissing refuses it, and
// PreflightUnverifiable asks the operator to confirm.
type PreflightStatus string

const (
	// PreflightOK: the source was checked and exists.
	PreflightOK PreflightStatus = "ok"
	// PreflightMissing: the source was checked and is absent. A mount would
	// create it, so the operation must be refused instead.
	PreflightMissing PreflightStatus = "missing"
	// PreflightUnverifiable: the source cannot be checked from inside the
	// panel, and continuing may create it as root:root 0755.
	PreflightUnverifiable PreflightStatus = "unverifiable"
)

// Preflight is the result of checking one Volume's source before an operation
// mounts it. Warning is the operator-facing explanation for a missing or
// unverifiable source, and empty for PreflightOK.
type Preflight struct {
	Status  PreflightStatus
	Warning string
}

// Preflight checks one Volume's source the only way its kind allows: a named
// volume exactly with `docker volume inspect`, a bind inside the data
// directory with os.Stat (the identical-path mount, ADR-0002), and a bind
// outside it not at all — only a mount would tell, and a mount would create
// it. When the check itself fails, the error is returned; a source that is
// merely absent is a PreflightMissing result, not an error.
func (r *Resolver) Preflight(ctx context.Context, volume Volume) (Preflight, error) {
	switch volume.Kind {
	case VolumeNamed:
		exists, err := r.cli.VolumeExists(ctx, volume.Source)
		if err != nil {
			return Preflight{}, fmt.Errorf("check named volume %q: %w", volume.Source, err)
		}
		if !exists {
			return Preflight{
				Status:  PreflightMissing,
				Warning: fmt.Sprintf("named volume %q does not exist; it may have been deleted", volume.Source),
			}, nil
		}
		return Preflight{Status: PreflightOK}, nil

	case VolumeBind:
		if !insideDataDir(r.dataDir, volume.Source) {
			return Preflight{
				Status: PreflightUnverifiable,
				Warning: fmt.Sprintf(
					"cannot check %q from the panel: it is outside the data directory, and continuing will create it as root:root 0755",
					volume.Source,
				),
			}, nil
		}
		if _, err := os.Stat(volume.Source); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return Preflight{
					Status:  PreflightMissing,
					Warning: fmt.Sprintf("bind source %q does not exist; refusing rather than letting Docker create it", volume.Source),
				}, nil
			}
			return Preflight{}, fmt.Errorf("check bind source %q: %w", volume.Source, err)
		}
		return Preflight{Status: PreflightOK}, nil

	default:
		return Preflight{}, fmt.Errorf("unknown volume kind %q", volume.Kind)
	}
}

// insideDataDir reports whether source sits at or under the panel's data
// directory — the one host location bind-mounted at its identical path
// (ADR-0002) — without being fooled by a sibling whose name merely shares the
// prefix.
func insideDataDir(dataDir, source string) bool {
	if dataDir == "" {
		return false
	}
	rel, err := filepath.Rel(dataDir, source)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
