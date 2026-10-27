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

type UserKind string

const (
	UserService UserKind = "service"
	UserPanel   UserKind = "panel"
	UserTraefik UserKind = "traefik"
	UserScanner UserKind = "scanner"
)

// ImageUser is what runs an image: a Service of a Compose, or one of the
// panel's own containers.
type ImageUser struct {
	Kind          UserKind `json:"kind"`
	ProjectID     int64    `json:"projectId,omitempty"`
	EnvironmentID int64    `json:"environmentId,omitempty"`
	ComposeID     int64    `json:"composeId,omitempty"`
	Service       string   `json:"service,omitempty"`
}

type UsedImage struct {
	ImageID string
	Refs    []string
	Users   []ImageUser
}

// InUse reads which images the running containers use, live from Docker.
type InUse interface {
	Images(ctx context.Context) ([]UsedImage, error)
}

// DeployGate is the Deployment queue as the scan worker sees it.
type DeployGate interface {
	// Busy is true while a Deployment runs or the queue is held for a Migration.
	Busy() bool
}
