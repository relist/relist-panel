package backupdest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func sftpStore(t *testing.T) (*Store, *sql.DB, int64) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	res, err := db.Exec(`INSERT INTO ssh_keys (name, private_key, public_key, created_at) VALUES ('deploy', 'PRIV', 'ssh-ed25519 PUB', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	keyID, _ := res.LastInsertId()
	return NewStore(db), db, keyID
}

func newSFTP(t *testing.T, s *Store, keyID int64) Destination {
	t.Helper()
	d, err := s.Create(context.Background(), Destination{
		Name: "box", Type: TypeSFTP, SSHKeyID: &keyID,
		SFTP: SFTPConfig{Host: "box", Port: 22, User: "u", Path: "/b", Auth: AuthKey},
	}, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStoreSFTPRoundTrip(t *testing.T) {
	s, _, keyID := sftpStore(t)
	d := newSFTP(t, s, keyID)
	got, err := s.Get(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SFTP != (SFTPConfig{Host: "box", Port: 22, User: "u", Path: "/b", Auth: AuthKey}) {
		t.Fatalf("sftp = %+v", got.SFTP)
	}
	if got.SSHKeyID == nil || *got.SSHKeyID != keyID || got.SSHKeyName != "deploy" || got.PrivateKey != "PRIV" || got.PublicKey != "ssh-ed25519 PUB" || got.HostKey != "" {
		t.Fatalf("got %+v", got)
	}
	list, err := s.List(context.Background())
	if err != nil || len(list) != 1 || list[0].PrivateKey != "PRIV" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestSetHostKeyOnlyOnce(t *testing.T) {
	s, _, keyID := sftpStore(t)
	d := newSFTP(t, s, keyID)
	ctx := context.Background()
	if err := s.SetHostKey(ctx, d.ID, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHostKey(ctx, d.ID, "B"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.HostKey != "A" {
		t.Fatalf("HostKey = %q", got.HostKey)
	}
}

func TestUpdateHostChangeForgetsHostKey(t *testing.T) {
	s, _, keyID := sftpStore(t)
	d := newSFTP(t, s, keyID)
	ctx := context.Background()
	_ = s.SetHostKey(ctx, d.ID, "A")
	d.SFTP.Path = "/other"
	if err := s.Update(ctx, d, time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.HostKey != "A" {
		t.Fatalf("a path change forgot the key: %q", got.HostKey)
	}
	d.SFTP.Port = 2222
	if err := s.Update(ctx, d, time.Unix(12, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.HostKey != "" {
		t.Fatalf("a port change kept the key: %q", got.HostKey)
	}
	_ = s.SetHostKey(ctx, d.ID, "A")
	d.SFTP.Host = "elsewhere"
	if err := s.Update(ctx, d, time.Unix(13, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.HostKey != "" {
		t.Fatalf("a host change kept the key: %q", got.HostKey)
	}
}

func TestUpdateToKeyAuthClearsSecret(t *testing.T) {
	s, _, keyID := sftpStore(t)
	ctx := context.Background()
	d, err := s.Create(ctx, Destination{Name: "pw", Type: TypeSFTP, Secret: "pw-123",
		SFTP: SFTPConfig{Host: "box", Port: 22, User: "u", Auth: AuthPassword}}, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	d.SFTP.Auth, d.SSHKeyID, d.Secret = AuthKey, &keyID, ""
	if err := s.Update(ctx, d, time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Secret != "" {
		t.Fatalf("Secret = %q", got.Secret)
	}
}

func TestResetHostKey(t *testing.T) {
	s, _, keyID := sftpStore(t)
	d := newSFTP(t, s, keyID)
	ctx := context.Background()
	_ = s.SetHostKey(ctx, d.ID, "A")
	_ = s.SetTestResult(ctx, d.ID, true, "", time.Unix(20, 0))
	if err := s.ResetHostKey(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, d.ID)
	if got.HostKey != "" || got.LastTestAt != nil || got.LastTestOK || got.LastTestError != "" {
		t.Fatalf("got %+v", got)
	}
	if err := s.ResetHostKey(ctx, 999); err != ErrNotFound {
		t.Fatalf("missing = %v", err)
	}
}

func TestDeleteSSHKeyRestricted(t *testing.T) {
	s, db, keyID := sftpStore(t)
	newSFTP(t, s, keyID)
	if _, err := db.Exec(`DELETE FROM ssh_keys WHERE id = ?`, keyID); err == nil {
		t.Fatal("a used key was deleted")
	}
}
