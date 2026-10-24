package composes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrAmbiguousIdentity is wrapped by requireUniqueIdentity when a legacy deploy
// identity is shared by two Composes, so a caller can tell that refusal from a
// lookup failure.
var ErrAmbiguousIdentity = errors.New("ambiguous deploy identity")

// ambiguousIdentity carries the refusal's own message while unwrapping to
// ErrAmbiguousIdentity, so the text callers already show is unchanged.
type ambiguousIdentity struct{ message string }

func (e ambiguousIdentity) Error() string { return e.message }
func (e ambiguousIdentity) Unwrap() error { return ErrAmbiguousIdentity }

// requireUniqueIdentity keeps ambiguous legacy deployments read-only. Moving a
// running stack would orphan it; the operator must resolve its old identity first.
func (s *Service) requireUniqueIdentity(ctx context.Context, compose Compose) error {
	if compose.IdentityVersion == 1 {
		return nil
	}
	rows, err := s.repository.db.QueryContext(ctx, `SELECT id, project_slug, env_slug, name FROM composes WHERE identity_version = 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var other Compose
		if err := rows.Scan(&other.ID, &other.ProjectSlug, &other.EnvSlug, &other.Name); err != nil {
			return err
		}
		if other.ID != compose.ID && other.DeployProjectName() == compose.DeployProjectName() {
			return ambiguousIdentity{fmt.Sprintf("legacy deploy identity %q is shared by Composes %d and %d; resolve the collision before operating on either Compose", compose.DeployProjectName(), compose.ID, other.ID)}
		}
	}
	return rows.Err()
}

// TeardownProject and TeardownEnvironment remove child infrastructure before a
// parent handler deletes its row. HTTP mutation coordination holds the Project
// gate across both teardown and metadata deletion, including child creation.
func (s *Service) TeardownProject(ctx context.Context, projectID int64) error {
	return s.teardownChildren(ctx, projectID, 0)
}
func (s *Service) TeardownEnvironment(ctx context.Context, projectID, environmentID int64) error {
	return s.teardownChildren(ctx, projectID, environmentID)
}
func (s *Service) teardownChildren(ctx context.Context, projectID, environmentID int64) error {
	rows, err := s.repository.db.QueryContext(ctx, `SELECT c.id,c.environment_id,c.name,c.project_slug,c.env_slug,c.yaml_content,c.created_at,c.updated_at,c.deployed_at,c.identity_version,c.domains_changed_at,c.variables_changed_at FROM composes c JOIN environments e ON e.id=c.environment_id WHERE e.project_id=? AND (?=0 OR e.id=?) ORDER BY c.id`, projectID, environmentID, environmentID)
	if err != nil {
		return err
	}
	var children []Compose
	for rows.Next() {
		c, err := scanComposeValues(rows.Scan)
		if err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Check all identities before tearing down even the first child.
	for _, c := range children {
		if err := s.requireUniqueIdentity(ctx, c); err != nil {
			return err
		}
	}
	ids := make([]int64, len(children))
	for i, c := range children {
		ids[i] = c.ID
	}
	s.cancelDeployments(ctx, ids...)
	for _, c := range children {
		if err := s.teardown(ctx, c, runRequestFor(s.dataDir, c), ids); err != nil {
			return fmt.Errorf("compose %d: %w", c.ID, err)
		}
	}
	return nil
}
func (s *Service) teardown(ctx context.Context, c Compose, req RunRequest, together []int64) error {
	if _, err := os.Stat(req.ComposeFilePath); err == nil {
		if _, _, err := s.destroyStack(ctx, c, req, together); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(filepath.Dir(req.ComposeFilePath))
}
