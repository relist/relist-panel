package databases

import (
	"context"
	"errors"

	"relist-panel/internal/directaccess"
)

// DirectAccessCloser closes a target's Direct accesses. Deleting a Database
// does it first, so no proxy is left pointing at a container that is gone
// (ADR-0041).
type DirectAccessCloser interface {
	CloseTarget(ctx context.Context, kind string, id int64) error
}

// DirectTarget makes a Database a Direct access target: kind "database",
// reached as relist-db-<id> on relist-network at the engine's port.
// directaccess never imports this package; the Service is handed this.
func (s *Service) DirectTarget() directaccess.Targets { return directTargets{s} }

type directTargets struct{ s *Service }

func (t directTargets) Resolve(ctx context.Context, kind string, id int64) (directaccess.Endpoint, error) {
	if kind != directaccess.KindDatabase {
		return directaccess.Endpoint{}, directaccess.ErrTargetNotFound
	}
	d, err := t.s.d.Store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return directaccess.Endpoint{}, directaccess.ErrTargetNotFound
	}
	if err != nil {
		return directaccess.Endpoint{}, err
	}
	if d.DeletedAt != nil {
		return directaccess.Endpoint{}, directaccess.ErrTargetNotFound
	}
	e, err := Lookup(d.Engine, d.Version)
	if err != nil {
		return directaccess.Endpoint{}, err
	}
	return directaccess.Endpoint{
		Name: d.Name, Network: Network, Host: ContainerName(id), Port: e.Port(),
		Running: t.s.view(ctx, d).Status == StatusRunning,
	}, nil
}
