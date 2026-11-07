package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"relist-panel/internal/composes"
	"relist-panel/internal/registry"
	"relist-panel/internal/traefikconfig"
	"relist-panel/internal/vulnscan"
)

// newRegistryService builds the Panel registry (ADR-0033). Without a Docker
// seam (tests) it still answers its hostname and logins; only its routes and
// reconcile need RegistryHost.
func newRegistryService(db *sql.DB, dataDir string, d Deps, composesService *composes.Service) *registry.Service {
	return registry.New(registry.Deps{
		Store:       registry.NewStore(db),
		Host:        d.RegistryHost,
		Files:       registry.Files{Dir: filepath.Join(dataDir, ".registry")},
		PanelDomain: d.PanelDomain,
		DomainHosts: func(ctx context.Context) (map[string]string, error) { return domainHosts(ctx, db) },
		ComposesUsing: func(ctx context.Context, host string) ([]string, error) {
			refs, err := composesService.ImageRefs(ctx)
			if err != nil {
				return nil, err
			}
			var names []string
			for ref, users := range refs {
				if !strings.HasPrefix(ref, host+"/") {
					continue
				}
				for _, u := range users {
					if !slices.Contains(names, u.Name) {
						names = append(names, u.Name)
					}
				}
			}
			slices.Sort(names)
			return names, nil
		},
	})
}

// domainHosts maps every Domain host to its Project's name.
func domainHosts(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT d.host, p.name
		FROM domains d
		JOIN composes c ON c.id = d.compose_id
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var host, project string
		if err := rows.Scan(&host, &project); err != nil {
			return nil, err
		}
		out[strings.ToLower(host)] = project
	}
	return out, rows.Err()
}

// registryRefs are the in-use sources the tags view and cleanup share: each
// Compose's latest successful deploy, and what running containers hold.
func registryRefs(d Deps, composesService *composes.Service) (deployed, running func(ctx context.Context) (map[string][]registry.User, error)) {
	deployed = func(ctx context.Context) (map[string][]registry.User, error) {
		refs, err := composesService.ImageRefs(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string][]registry.User{}
		for ref, users := range refs {
			for _, u := range users {
				out[ref] = append(out[ref], registry.User{ComposeID: u.ComposeID, ComposeName: u.Name, ProjectID: u.ProjectID,
					ProjectName: u.ProjectName, EnvironmentID: u.EnvironmentID, EnvironmentName: u.EnvironmentName})
			}
		}
		return out, nil
	}
	if d.VulnInUse != nil {
		running = func(ctx context.Context) (map[string][]registry.User, error) {
			images, err := d.VulnInUse.Images(ctx)
			if err != nil {
				return nil, err
			}
			out := map[string][]registry.User{}
			for _, image := range images {
				for _, u := range image.Users {
					if u.Kind != vulnscan.UserService {
						continue
					}
					for _, ref := range image.Refs {
						out[ref] = append(out[ref], registry.User{ComposeID: u.ComposeID, ProjectID: u.ProjectID, EnvironmentID: u.EnvironmentID})
					}
				}
			}
			return out, nil
		}
	}
	return deployed, running
}

// inUseRefs flattens the in-use sources for cleanup's retention.
func inUseRefs(sources ...func(ctx context.Context) (map[string][]registry.User, error)) func(ctx context.Context) (map[string]bool, error) {
	return func(ctx context.Context) (map[string]bool, error) {
		out := map[string]bool{}
		for _, source := range sources {
			if source == nil {
				continue
			}
			refs, err := source(ctx)
			if err != nil {
				return nil, err
			}
			for ref, users := range refs {
				if len(users) > 0 {
					out[ref] = true
				}
			}
		}
		return out, nil
	}
}

// registryGate holds cleanup off while a Deployment runs, and while a Panel
// update or a Migration holds the lock. A lock it cannot read counts as held.
type registryGate struct {
	busy   func() bool
	locked func() bool
}

func (g registryGate) Busy() bool   { return g.busy() }
func (g registryGate) Locked() bool { return g.locked() }

func registryAPI(d Deps, dataDir string, svc *registry.Service, composesService *composes.Service, cleaner *registry.Cleaner) *registry.API {
	deployed, running := registryRefs(d, composesService)
	return registry.NewAPI(registry.APIDeps{
		Service:      svc,
		Cleaner:      cleaner,
		DeployedRefs: deployed,
		RunningRefs:  running,
		ReadTimeout: func(context.Context) (string, error) {
			raw, err := os.ReadFile(traefikconfig.PathsFor(dataDir).Override())
			if errors.Is(err, os.ErrNotExist) {
				return traefikconfig.DefaultReadTimeout, nil
			}
			if err != nil {
				return "", err
			}
			form, _, err := traefikconfig.ReadForm(raw)
			return form.ReadTimeout, err
		},
	})
}
