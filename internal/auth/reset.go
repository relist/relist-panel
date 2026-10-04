package auth

import (
	"context"
	"database/sql"
	"errors"
)

var (
	// ErrAccountNotFound reports that no admin row matches the requested
	// email, or that there is no account to pick when none was given.
	ErrAccountNotFound = errors.New("admin account not found")
	// ErrEmailRequired reports that no email was given and more than one
	// account exists, so the caller must choose one explicitly.
	ErrEmailRequired = errors.New("email is required")
)

// ResetResult records what ResetTwoFactor changed, so the command can print
// it.
type ResetResult struct {
	Email           string
	WasEnabled      bool
	SessionsRevoked int64
}

// ResetTwoFactor is the recovery path for a lost authenticator. It clears the
// account's TOTP state and its sign-in lock, and revokes every session. All
// writes share one transaction, so the reset is all-or-nothing. An empty
// email selects the only account; with more than one it returns
// ErrEmailRequired.
func ResetTwoFactor(ctx context.Context, db *sql.DB, email string) (ResetResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ResetResult{}, err
	}
	defer tx.Rollback()

	account, err := findResetTarget(ctx, tx, email)
	if err != nil {
		return ResetResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_credentials
		SET totp_secret = NULL,
		    totp_pending_secret = NULL,
		    totp_last_step = NULL,
		    failed_attempts = 0,
		    locked_until = NULL
		WHERE id = ?
	`, account.id); err != nil {
		return ResetResult{}, err
	}

	deleted, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE admin_id = ?", account.id)
	if err != nil {
		return ResetResult{}, err
	}
	revoked, err := deleted.RowsAffected()
	if err != nil {
		return ResetResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return ResetResult{}, err
	}

	return ResetResult{
		Email:           account.email,
		WasEnabled:      account.totpSecret.Valid,
		SessionsRevoked: revoked,
	}, nil
}

type resetTarget struct {
	id         int64
	email      string
	totpSecret sql.NullString
}

// findResetTarget resolves the account to reset. An email matches at most one
// row because admin_credentials.email is UNIQUE. Without an email the query
// must find exactly one account.
func findResetTarget(ctx context.Context, tx *sql.Tx, email string) (resetTarget, error) {
	if email != "" {
		var account resetTarget
		err := tx.QueryRowContext(ctx, `
			SELECT id, email, totp_secret
			FROM admin_credentials
			WHERE email = ?
		`, email).Scan(&account.id, &account.email, &account.totpSecret)
		if errors.Is(err, sql.ErrNoRows) {
			return resetTarget{}, ErrAccountNotFound
		}
		if err != nil {
			return resetTarget{}, err
		}
		return account, nil
	}

	rows, err := tx.QueryContext(ctx, "SELECT id, email, totp_secret FROM admin_credentials")
	if err != nil {
		return resetTarget{}, err
	}
	defer rows.Close()

	var accounts []resetTarget
	for rows.Next() {
		var account resetTarget
		if err := rows.Scan(&account.id, &account.email, &account.totpSecret); err != nil {
			return resetTarget{}, err
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return resetTarget{}, err
	}

	switch len(accounts) {
	case 0:
		return resetTarget{}, ErrAccountNotFound
	case 1:
		return accounts[0], nil
	default:
		return resetTarget{}, ErrEmailRequired
	}
}
