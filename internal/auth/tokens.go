package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	tokenInvite = "invite"
	tokenReset  = "reset"

	inviteLifetime = 7 * 24 * time.Hour
	resetLifetime  = 24 * time.Hour
)

// issueToken creates the one open invite or reset link of userID, deleting
// any earlier one, and returns the token itself: only its hash is stored, so
// this is the only time anyone sees it.
func issueToken(ctx context.Context, tx *sql.Tx, userID int64, kind string, createdBy int64, now time.Time) (string, time.Time, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", time.Time{}, fmt.Errorf("generate link token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	lifetime := inviteLifetime
	if kind == tokenReset {
		lifetime = resetLifetime
	}
	expiresAt := now.UTC().Add(lifetime)
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_tokens WHERE user_id = ? AND used_at IS NULL", userID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_tokens (user_id, kind, token_hash, expires_at, created_by)
		VALUES (?, ?, ?, ?, ?)
	`, userID, kind, hashToken(token), expiresAt.Unix(), createdBy); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// hashToken is how a link token is stored and looked up.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
