package policy

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Approver is who may approve a violating config (RELIST_POLICY_APPROVER).
type Approver string

const (
	ApproverAdmins Approver = "admins"
	ApproverOwner  Approver = "owner"
)

// ownerID is the Owner's users.id (ADR-0031).
const ownerID = 1

// ParseApprover reads RELIST_POLICY_APPROVER: admins (also when empty) or
// owner. Anything else is an error, so a typo cannot widen it.
func ParseApprover(value string) (Approver, error) {
	switch Approver(strings.ToLower(strings.TrimSpace(value))) {
	case "", ApproverAdmins:
		return ApproverAdmins, nil
	case ApproverOwner:
		return ApproverOwner, nil
	}
	return "", fmt.Errorf("RELIST_POLICY_APPROVER must be admins or owner, not %q", value)
}

// Allows reports whether a User is an approver.
func (a Approver) Allows(userID int64, isAdmin bool) bool {
	if a == ApproverOwner {
		return userID == ownerID
	}
	return isAdmin
}

// ParseRoots reads RELIST_POLICY_BIND_ROOTS: comma-separated absolute host
// paths, cleaned. The root of the filesystem is refused: it would allow
// everything.
func ParseRoots(value string) ([]string, error) {
	roots := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !filepath.IsAbs(part) {
			return nil, fmt.Errorf("RELIST_POLICY_BIND_ROOTS: %q is not an absolute path", part)
		}
		clean := filepath.Clean(part)
		if clean == "/" {
			return nil, fmt.Errorf("RELIST_POLICY_BIND_ROOTS: / would allow every path")
		}
		roots = append(roots, clean)
	}
	return roots, nil
}
