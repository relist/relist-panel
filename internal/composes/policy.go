package composes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"relist-panel/internal/policy"
)

// privateSourcesDir is where a Deployment keeps its own copy of the Source
// (deployments.sourceSnapshotsDir); the policy reads paths there as the
// Compose directory's.
const privateSourcesDir = ".deployment-sources"

// PolicyResult is a Compose's config against the Compose policy (ADR-0032).
// Hash is what an approval is for; Approved says the stored approval is for
// this Hash.
type PolicyResult struct {
	Violations []policy.Violation
	Hash       string
	Approved   bool
}

// Passes reports whether the config may deploy: no violation, or approved.
func (r PolicyResult) Passes() bool { return len(r.Violations) == 0 || r.Approved }

// SetPolicyRules sets the install's bind roots and its own domain, which
// every evaluation checks against.
func (s *Service) SetPolicyRules(roots []string, panelDomain string) {
	s.policyRoots, s.panelDomain = roots, panelDomain
}

// EvaluatePolicy checks the config t deploys: its Snapshot, captured now when
// t has none. The snapshot is staged under unique names in the Compose
// directory and resolved by the runner; paths that change from one
// evaluation or Deployment to the next (the staging, a Deployment's private
// Source copy) are read as the paths they stand for, so the same saved
// config always has the same Hash. Any failure is an error, never a pass.
func (s *Service) EvaluatePolicy(ctx context.Context, t DeployTarget) (PolicyResult, error) {
	if t.Snapshot == nil {
		captured, err := s.Capture(ctx, t)
		if err != nil {
			return PolicyResult{}, err
		}
		t = captured
	}
	dir := ComposeDir(t.Request)
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return PolicyResult{}, err
	}
	stage := filepath.Join(dir, ".policy-"+hex.EncodeToString(token))
	req, cleanup, err := stageSnapshot(t, stage+".yml", stage+".env", stage+"-sets")
	if err != nil {
		return PolicyResult{}, fmt.Errorf("stage the config for the policy: %w", err)
	}
	out, err := s.runner.PolicyConfig(ctx, req)
	cleanup()
	if err != nil {
		return PolicyResult{}, fmt.Errorf("resolve the config for the policy: %w", err)
	}
	normalized, err := normalizePolicyPaths(out, map[string]string{
		stage + ".env":  t.Request.EnvFilePath,
		stage + "-sets": setsDir(t.Request.EnvFilePath),
	}, dir)
	if err != nil {
		return PolicyResult{}, err
	}
	violations, err := policy.Check(normalized, t.Compose.YAMLContent, policy.Rules{
		ComposeDir: dir, Roots: s.policyRoots, ProjectName: t.Request.ProjectName, PanelDomain: s.panelDomain,
	})
	if err != nil {
		return PolicyResult{}, err
	}
	hash, err := policy.Hash(normalized)
	if err != nil {
		return PolicyResult{}, err
	}
	approved, err := s.approvedHash(ctx, t.Compose.ID)
	if err != nil {
		return PolicyResult{}, err
	}
	return PolicyResult{Violations: violations, Hash: hash, Approved: approved == hash}, nil
}

// normalizePolicyPaths rewrites every string in the config that starts with
// a key of replace, or lies in a private Source copy under dir.
func normalizePolicyPaths(config []byte, replace map[string]string, dir string) ([]byte, error) {
	var doc any
	if err := json.Unmarshal(config, &doc); err != nil {
		return nil, fmt.Errorf("read resolved config: %w", err)
	}
	private := filepath.Join(dir, privateSourcesDir) + string(filepath.Separator)
	var walk func(any) any
	walk = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			for k, item := range v {
				v[k] = walk(item)
			}
		case []any:
			for i, item := range v {
				v[i] = walk(item)
			}
		case string:
			for from, to := range replace {
				if v == from || strings.HasPrefix(v, from+string(filepath.Separator)) {
					return to + strings.TrimPrefix(v, from)
				}
			}
			if rest, ok := strings.CutPrefix(v, private); ok {
				// <dir>/.deployment-sources/<copy>/<path> is <dir>/<path>.
				if _, inner, found := strings.Cut(rest, string(filepath.Separator)); found {
					return filepath.Join(dir, inner)
				}
				return dir
			}
		}
		return value
	}
	return json.Marshal(walk(doc))
}

func (s *Service) approvedHash(ctx context.Context, composeID int64) (string, error) {
	var hash string
	err := s.repository.db.QueryRowContext(ctx, "SELECT config_hash FROM compose_approvals WHERE compose_id = ?", composeID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

// Approve records an approver's consent to deploy the config with hash,
// replacing any earlier approval of the Compose.
func (s *Service) Approve(ctx context.Context, composeID int64, hash string, userID int64) error {
	var by any
	if userID > 0 {
		by = userID
	}
	_, err := s.repository.db.ExecContext(ctx, `INSERT INTO compose_approvals (compose_id, config_hash, approved_by, approved_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (compose_id) DO UPDATE SET config_hash = excluded.config_hash, approved_by = excluded.approved_by, approved_at = excluded.approved_at`,
		composeID, hash, by, s.now().Unix())
	return err
}

// VolumeAllowed reports whether the Volume Explorer may open a Volume of the
// deployed file req points at: when no violation of that file names it (its
// bind source, or its named volume key), or for an approver when the
// Compose's current config is approved. Any failure answers false.
func (s *Service) VolumeAllowed(ctx context.Context, composeID int64, req RunRequest, bind bool, source, logical string, approver bool) (bool, error) {
	out, err := s.runner.PolicyConfig(ctx, req)
	if err != nil {
		return false, fmt.Errorf("resolve the deployed config for the policy: %w", err)
	}
	violations, err := policy.Check(out, "", policy.Rules{
		ComposeDir: ComposeDir(req), Roots: s.policyRoots, ProjectName: req.ProjectName, PanelDomain: s.panelDomain,
	})
	if err != nil {
		return false, err
	}
	named := false
	for _, v := range violations {
		if bind && v.Bind != "" && filepath.Clean(v.Bind) == filepath.Clean(source) || !bind && v.Volume != "" && v.Volume == logical {
			named = true
			break
		}
	}
	if !named {
		return true, nil
	}
	if !approver {
		return false, nil
	}
	t, err := s.Target(ctx, composeID)
	if err != nil {
		return false, err
	}
	result, err := s.EvaluatePolicy(ctx, t)
	if err != nil {
		return false, err
	}
	return result.Approved, nil
}
