package composes

import (
	"context"
	"slices"
	"sort"
	"strings"
)

// ComposeRef names a Compose that uses an image.
type ComposeRef struct {
	ComposeID       int64  `json:"composeId"`
	Name            string `json:"composeName"`
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
}

// setImageRefs replaces the image references of a Compose's latest
// successful deploy.
func (r *repository) setImageRefs(ctx context.Context, composeID int64, refs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM compose_image_refs WHERE compose_id = ?`, composeID); err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO compose_image_refs (compose_id, ref) VALUES (?, ?)`, composeID, ref); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ImageRefs maps each image reference a Compose's latest successful deploy
// used to the Composes that used it, ordered by Project, Environment and name.
func (s *Service) ImageRefs(ctx context.Context) (map[string][]ComposeRef, error) {
	rows, err := s.repository.db.QueryContext(ctx, `
		SELECT r.ref, c.id, c.name, p.id, p.name, e.id, e.name
		FROM compose_image_refs r
		JOIN composes c ON c.id = r.compose_id
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		ORDER BY p.name, e.name, c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]ComposeRef{}
	for rows.Next() {
		var ref string
		var c ComposeRef
		if err := rows.Scan(&ref, &c.ComposeID, &c.Name, &c.ProjectID, &c.ProjectName, &c.EnvironmentID, &c.EnvironmentName); err != nil {
			return nil, err
		}
		for _, key := range normaliseImageRef(ref) {
			if !slices.ContainsFunc(out[key], func(u ComposeRef) bool { return u.ComposeID == c.ComposeID }) {
				out[key] = append(out[key], c)
			}
		}
	}
	return out, rows.Err()
}

// normaliseImageRef spells a ref the way a registry lists it: an implicit
// tag becomes :latest, a registry host is lowercased, and name:tag@digest
// counts as both name:tag and name@digest. Refs are recorded as the Compose
// file wrote them, so lookups by host/repo:tag need this.
func normaliseImageRef(ref string) []string {
	name, digest := ref, ""
	if i := strings.Index(ref, "@"); i >= 0 {
		name, digest = ref[:i], ref[i:]
	}
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		name = strings.ToLower(first) + "/" + rest
	}
	hasTag := strings.Contains(name[strings.LastIndex(name, "/")+1:], ":")
	base := name
	if hasTag {
		base = name[:strings.LastIndex(name, ":")]
	}
	switch {
	case digest != "" && hasTag:
		return []string{name, base + digest}
	case digest != "":
		return []string{base + digest}
	case hasTag:
		return []string{name}
	default:
		return []string{name + ":latest"}
	}
}

// sortedUnique is refs without repeats, sorted.
func sortedUnique(refs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range refs {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}
