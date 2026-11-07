package registry

import (
	"context"
	"errors"
	"regexp"
)

// ErrInvalidName is a Push credential name the registry cannot take.
var ErrInvalidName = errors.New("name must be 1–40 lowercase letters, digits, - or _, starting with a letter or digit, and not relist-panel")

var credentialName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// CreateCredential makes a Push credential and returns its token, which is
// never stored: only its bcrypt hash is, in the database and htpasswd. The
// registry reads the new htpasswd without a restart (probed 2026-11-06).
func (s *Service) CreateCredential(ctx context.Context, name, createdBy string) (Credential, string, error) {
	if !credentialName.MatchString(name) || name == SystemUser {
		return Credential{}, "", ErrInvalidName
	}
	token, hash, err := NewToken()
	if err != nil {
		return Credential{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.d.Store.CreateCredential(ctx, Credential{Name: name, Hash: hash, CreatedBy: createdBy, CreatedAt: s.d.Now()})
	if err != nil {
		return Credential{}, "", err
	}
	return c, token, s.rewriteLogins(ctx)
}

// DeleteCredential revokes a Push credential at once.
func (s *Service) DeleteCredential(ctx context.Context, id int64) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.d.Store.DeleteCredential(ctx, id)
	if err != nil {
		return Credential{}, err
	}
	return c, s.rewriteLogins(ctx)
}

// rewriteLogins rewrites the files once the Registry has a system login;
// before its first Enable there is nothing to serve them.
func (s *Service) rewriteLogins(ctx context.Context) error {
	st, err := s.d.Store.Settings(ctx)
	if err != nil || st.SystemHash == "" {
		return err
	}
	return s.writeFiles(ctx, st)
}
