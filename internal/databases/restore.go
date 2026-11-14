package databases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

var ErrChangedFile = errors.New("the backup file changed since it was written; it was not restored")

// fileSHA is the sha256 of the file at path, hex.
func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RestoreFromBackup and RestoreFromUpload are replaced in the next commits
// (plan-phase-2b Task 3).
func (s *Service) RestoreFromBackup(ctx context.Context, id, backupID int64, target, confirm string) (Restore, error) {
	return Restore{}, errors.New("not implemented")
}

func (s *Service) RestoreFromUpload(ctx context.Context, id int64, target, confirm, name string, body io.Reader) (Restore, error) {
	return Restore{}, errors.New("not implemented")
}
