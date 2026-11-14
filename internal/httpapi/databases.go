package httpapi

import (
	"context"
	"database/sql"
	"errors"

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

// databaseVolumes is the Migration manifest's source of Database volumes.
// Nil without Databases.
func databaseVolumes(svc *databases.Service) func(ctx context.Context) ([]string, error) {
	if svc == nil {
		return nil
	}
	return svc.Volumes
}

// beforeDeploy runs the Registry's start, then the wanted Databases', before a
// Migration's Deployments: an app that needs a Database finds it up. Both run
// even when the first fails, and their errors join. Nil when there is neither.
func beforeDeploy(registryStart func(context.Context) error, svc *databases.Service) func(context.Context) error {
	if registryStart == nil && svc == nil {
		return nil
	}
	return func(ctx context.Context) error {
		var errs []error
		if registryStart != nil {
			errs = append(errs, registryStart(ctx))
		}
		if svc != nil {
			errs = append(errs, svc.StartWanted(ctx))
		}
		return errors.Join(errs...)
	}
}
