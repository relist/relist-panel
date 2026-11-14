package httpapi

import (
	"context"
	"database/sql"

	"relist-panel/internal/databases"
	"relist-panel/internal/imageref"
)

// newDatabaseService builds the panel's Databases (ADR-0039). Nil without a
// Docker host for them. locked is the Migration lock; a lock it cannot read
// counts as held, as the Registry's gate does.
func newDatabaseService(db *sql.DB, d Deps, locked func(ctx context.Context) (bool, error)) *databases.Service {
	if d.DatabaseHost == nil {
		return nil
	}
	return databases.New(databases.Deps{
		Store: databases.NewStore(db),
		Host:  d.DatabaseHost,
		Locked: func(ctx context.Context) bool {
			l, err := locked(ctx)
			return l || err != nil
		},
	})
}

// ownDatabases marks every Database's volume and image as the Databases', so
// the Docker page never offers them to a cleanup.
func ownDatabases(ctx context.Context, svc *databases.Service, images, volumes map[string]string) error {
	views, err := svc.List(ctx)
	if err != nil {
		return err
	}
	for _, v := range views {
		volumes[databases.VolumeName(v.ID)] = databases.OwnerName
		if e, ok := databases.Engines[v.Engine]; ok {
			for _, ref := range imageref.Pinned(e.Image(v.Version)) {
				images[ref] = databases.OwnerName
			}
		}
	}
	return nil
}
