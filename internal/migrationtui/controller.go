package migrationtui

import (
	"context"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationctl"
)

// Controller is what the TUIs need from the panel; *migrationctl.Client is
// the one over the control socket, and the tests fake it.
type Controller interface {
	Progress(ctx context.Context) (migration.Progress, error)
	Connect(ctx context.Context, sourceURL, token, selfURL string) (migration.Progress, error)
	Recheck(ctx context.Context, hostPaths []string) (migration.Progress, error)
	Start(ctx context.Context, hostPaths []string) error
	Retry(ctx context.Context) error
	Abort(ctx context.Context) error
	CreateToken(ctx context.Context, lifetime string) (migrationctl.Token, error)
	RevokeToken(ctx context.Context) error
	Status(ctx context.Context) (migration.SourceStatus, error)
	Resume(ctx context.Context) (warning string, err error)
	Activity(ctx context.Context) (migration.Activity, error)
}

var _ Controller = (*migrationctl.Client)(nil)
