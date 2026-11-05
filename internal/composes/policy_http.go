package composes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
	"relist-panel/internal/policy"
)

// SetApprover sets who may approve a violating config
// (RELIST_POLICY_APPROVER); the zero value is every admin.
func (s *Service) SetApprover(a policy.Approver) { s.approver = a }

// IsApprover reports whether a User may approve a violating config.
func (s *Service) IsApprover(u auth.User) bool {
	return s.approver.Allows(u.ID, u.IsAdmin)
}

// CurrentUserIsApprover reports whether the request's User is an approver.
func (s *Service) CurrentUserIsApprover(ctx context.Context) bool {
	u, ok := auth.UserFrom(ctx)
	return ok && s.IsApprover(u)
}

// PolicyView is a PolicyResult as the API answers it: the hash only to an
// approver, since it is a hash over interpolated secret values.
type PolicyView struct {
	Violations []policy.Violation `json:"violations"`
	ConfigHash *string            `json:"configHash"`
	Approved   bool               `json:"approved"`
}

// View is the result as an approver (or not) may see it.
func (r PolicyResult) View(approver bool) PolicyView {
	v := PolicyView{Violations: r.Violations, Approved: r.Approved}
	if v.Violations == nil {
		v.Violations = []policy.Violation{}
	}
	if approver {
		hash := r.Hash
		v.ConfigHash = &hash
	}
	return v
}

// policyTarget loads the Compose a policy route names, answering the error
// itself when it cannot.
func (s *Service) policyTarget(w http.ResponseWriter, r *http.Request) (DeployTarget, bool) {
	_, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return DeployTarget{}, false
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return DeployTarget{}, false
	}
	if _, err := s.repository.findByID(r.Context(), environmentID, id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return DeployTarget{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return DeployTarget{}, false
	}
	t, err := s.Target(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return DeployTarget{}, false
	}
	return t, true
}

// evaluateFor evaluates t for a policy route, answering a failure itself:
// a Compose that cannot be resolved is the user's to fix (422).
func (s *Service) evaluateFor(w http.ResponseWriter, r *http.Request, t DeployTarget) (PolicyResult, bool) {
	result, err := s.EvaluatePolicy(r.Context(), t)
	if err != nil {
		var f *DeployFailure
		if errors.Is(err, ErrBlankYAML) || errors.As(err, &f) && f.Kind == FailureInvalid {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return PolicyResult{}, false
		}
		log.Printf("composes: policy for compose %d: %v", t.Compose.ID, err)
		writeError(w, http.StatusBadGateway, "could not check the compose against the policy: "+err.Error())
		return PolicyResult{}, false
	}
	return result, true
}

// getPolicy answers the Compose's violations, whether its config is
// approved, and the hash to an approver.
func (s *Service) getPolicy(w http.ResponseWriter, r *http.Request) {
	t, ok := s.policyTarget(w, r)
	if !ok {
		return
	}
	result, ok := s.evaluateFor(w, r, t)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, result.View(s.CurrentUserIsApprover(r.Context())))
}

// approve records an approver's consent to the config they were shown:
// 409 when configHash is no longer the Compose's, so an edit saved between
// looking and clicking is never approved unseen.
func (s *Service) approve(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || !s.IsApprover(user) {
		writeError(w, http.StatusForbidden, "Only an approver can approve a Compose")
		return
	}
	var in struct {
		ConfigHash string `json:"configHash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || in.ConfigHash == "" {
		writeError(w, http.StatusBadRequest, "configHash is required")
		return
	}
	t, ok := s.policyTarget(w, r)
	if !ok {
		return
	}
	result, ok := s.evaluateFor(w, r, t)
	if !ok {
		return
	}
	if result.Hash != in.ConfigHash {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "The Compose changed since you looked; review it again", "policy": result.View(true)})
		return
	}
	if err := s.Approve(r.Context(), t.Compose.ID, result.Hash, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the approval")
		return
	}
	audit.Detail(r.Context(), "violations", len(result.Violations))
	w.WriteHeader(http.StatusNoContent)
}
