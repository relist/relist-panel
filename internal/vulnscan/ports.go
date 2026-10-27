package vulnscan

import (
	"context"
	"errors"
)

// DBInfo identifies the vulnerability DB a scan ran against.
type DBInfo struct {
	Version string // the DB's UpdatedAt, RFC 3339
}

var (
	ErrNoDB      = errors.New("vulnerability DB not downloaded yet")
	ErrImageGone = errors.New("image no longer exists")
)

// Scanner runs the locked-down Scanner container (ADR-0021). ref is always a
// digest reference the operator or the built-in default chose.
type Scanner interface {
	EnsureImage(ctx context.Context, ref string) error
	UpdateDB(ctx context.Context, ref string) (DBInfo, error)
	DBInfo(ctx context.Context, ref string) (DBInfo, error)
	Scan(ctx context.Context, ref, imageID string) (Report, error)
	ImageID(ctx context.Context, ref string) (string, error)
	RemoveImage(ctx context.Context, ref string) error
	DeleteCache(ctx context.Context) error
	CacheSize(ctx context.Context) (int64, error)
	// Cleanup removes Scanner containers and exported tars a restart left.
	Cleanup(ctx context.Context) error
}
