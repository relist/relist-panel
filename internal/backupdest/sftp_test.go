package backupdest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func hostKeyLine(s ssh.Signer) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey())))
}

// clientKey makes a login key: its private PEM and its public half.
func clientKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block)), sp
}

func keyDest(t *testing.T, srv *testSFTP, pem, hostKey string) Destination {
	t.Helper()
	return Destination{Type: TypeSFTP, HostKey: hostKey, PrivateKey: pem,
		SFTP: SFTPConfig{Host: srv.host, Port: srv.port, User: "u", Auth: AuthKey}}
}

func pwDest(srv *testSFTP, pw, hostKey string) Destination {
	return Destination{Type: TypeSFTP, HostKey: hostKey, Secret: pw,
		SFTP: SFTPConfig{Host: srv.host, Port: srv.port, User: "u", Auth: AuthPassword}}
}

func remote(t *testing.T, d Destination) Remote {
	t.Helper()
	r, err := NewSFTP(d)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// trust runs Test against an unknown host and returns the key it recorded.
func trust(t *testing.T, d Destination) string {
	t.Helper()
	r := remote(t, d)
	if err := r.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	seen := r.(HostKeyRecorder).SeenHostKey()
	if seen == "" {
		t.Fatal("Test saw no host key")
	}
	return seen
}

func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func within(t *testing.T, d time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("did not return within %s", d)
		return nil
	}
}

func testPut(t *testing.T, srv *testSFTP, d Destination) {
	t.Helper()
	d.HostKey = trust(t, d)
	data := make([]byte, 1<<20)
	_, _ = rand.Read(data)
	key := "backups/app-3/main/x.sql.gz"
	if err := remote(t, d).Put(context.Background(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(srv.root, key))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("stored file: %v, equal=%v", err, bytes.Equal(got, data))
	}
	for _, f := range filesUnder(t, srv.root) {
		if strings.HasSuffix(f, ".part") {
			t.Fatalf("left %s", f)
		}
	}
}

func TestSFTPPutWithKey(t *testing.T) {
	priv, pub := clientKey(t)
	srv := startSFTP(t, sftpOpts{clientKey: pub})
	testPut(t, srv, keyDest(t, srv, priv, ""))
}

func TestSFTPPutWithPassword(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw-correct-1"})
	testPut(t, srv, pwDest(srv, "pw-correct-1", ""))
}

func TestSFTPTestRecordsUnknownKey(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw-correct-1"})
	if got := trust(t, pwDest(srv, "pw-correct-1", "")); got != hostKeyLine(srv.hostKeys[0]) {
		t.Fatalf("seen = %q, want %q", got, hostKeyLine(srv.hostKeys[0]))
	}
	r := remote(t, pwDest(srv, "wrong-pw-9876", ""))
	if err := r.Test(context.Background()); err == nil {
		t.Fatal("Test with a wrong password passed")
	}
	if got := r.(HostKeyRecorder).SeenHostKey(); got != "" {
		t.Fatalf("a failed Test saw %q", got)
	}
	// Test left nothing behind.
	if f := filesUnder(t, srv.root); len(f) != 0 {
		t.Fatalf("files left: %v", f)
	}
}

func TestSFTPHostKeyChanged(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw1"})
	other := newSigner(t, "ed25519")
	d := pwDest(srv, "pw1", hostKeyLine(other))
	ctx := context.Background()
	r := remote(t, d)
	for name, err := range map[string]error{
		"Test":   r.Test(ctx),
		"Put":    r.Put(ctx, "a/b", strings.NewReader("x"), 1),
		"Delete": r.Delete(ctx, "a/b"),
	} {
		if !errors.Is(err, ErrHostKeyChanged) {
			t.Errorf("%s: err = %v, want ErrHostKeyChanged", name, err)
		}
	}
	if f := filesUnder(t, srv.root); len(f) != 0 {
		t.Fatalf("files written: %v", f)
	}
}

func TestSFTPPutWithoutHostKey(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw1"})
	r := remote(t, pwDest(srv, "pw1", ""))
	if err := r.Put(context.Background(), "a", strings.NewReader("x"), 1); !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("Put = %v", err)
	}
	if err := r.Delete(context.Background(), "a"); !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("Delete = %v", err)
	}
	if n := srv.conns.Load(); n != 0 {
		t.Fatalf("server accepted %d connections", n)
	}
}

// Review Focus 2: with several host key types on the server, the recorded
// type is the one asked for.
func TestSFTPRecordedAlgorithmIsAsked(t *testing.T) {
	ed, rsaKey := newSigner(t, "ed25519"), newSigner(t, "rsa")
	srv := startSFTP(t, sftpOpts{password: "pw1", hostKeys: []ssh.Signer{ed, rsaKey}})
	// x/crypto asks for rsa-sha2 before ed25519 by default, so recording the
	// ed25519 key is the case a missing pin breaks; RSA covers the other way.
	for _, recorded := range []ssh.Signer{ed, rsaKey} {
		d := pwDest(srv, "pw1", hostKeyLine(recorded))
		for i := 0; i < 3; i++ {
			if err := remote(t, d).Put(context.Background(), "x/f", strings.NewReader("data"), 4); err != nil {
				t.Fatalf("Put %d with %s recorded: %v", i, recorded.PublicKey().Type(), err)
			}
		}
	}
	if got := hostKeyAlgorithms(rsaKey.PublicKey()); !reflect.DeepEqual(got, []string{"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa"}) {
		t.Fatalf("rsa = %v", got)
	}
	if got := hostKeyAlgorithms(ed.PublicKey()); !reflect.DeepEqual(got, []string{"ssh-ed25519"}) {
		t.Fatalf("ed25519 = %v", got)
	}
}

// Review Focus 1: a server that accepts TCP and never answers.
func TestSFTPHungServerTimesOut(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	host, port, _ := net.SplitHostPort(l.Addr().String())
	d := Destination{Type: TypeSFTP, Secret: "pw1",
		SFTP: SFTPConfig{Host: host, Port: atoiPort(port), User: "u", Auth: AuthPassword}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := within(t, 3*time.Second, func() error { return remote(t, d).Test(ctx) }); err == nil {
		t.Fatal("Test against a hung server passed")
	}
}

// The conn close stays armed after the handshake: the 5-minute stall cut
// depends on it.
func TestSFTPStalledAfterHandshake(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw1", stall: true})
	d := pwDest(srv, "pw1", hostKeyLine(srv.hostKeys[0]))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := within(t, 3*time.Second, func() error {
		return remote(t, d).Put(ctx, "a/b", strings.NewReader("data"), 4)
	})
	if err == nil {
		t.Fatal("Put against a stalled server passed")
	}
}

// Review Focus 4: an interrupted upload leaves no wrong-size file and no .part.
func TestSFTPPutFailureLeavesNothing(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw1"})
	d := pwDest(srv, "pw1", hostKeyLine(srv.hostKeys[0]))
	key := "backups/app-3/main/x.sql.gz"
	err := remote(t, d).Put(context.Background(), key, strings.NewReader("short"), 1000)
	if err == nil {
		t.Fatal("a short upload passed")
	}
	for _, name := range []string{key, key + ".part"} {
		if _, err := os.Stat(filepath.Join(srv.root, name)); err == nil {
			t.Fatalf("%s exists", name)
		}
	}
}

func TestRenameIntoFallback(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw1"})
	d := pwDest(srv, "pw1", hostKeyLine(srv.hostKeys[0]))
	c, closeFn, err := remote(t, d).(*sftpRemote).connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	write := func(name, body string) {
		f, err := c.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(body))
		f.Close()
	}
	write("target", "old")
	write("target.part", "new")
	if err := renameInto(c.Client, "target.part", "target", false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(srv.root, "target"))
	if string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if _, err := os.Stat(filepath.Join(srv.root, "target.part")); err == nil {
		t.Fatal("from still exists")
	}
	// With no target, the fallback still renames.
	write("t2.part", "v")
	if err := renameInto(c.Client, "t2.part", "t2", false); err != nil {
		t.Fatal(err)
	}
}

func TestSFTPDeleteMissingIsNotError(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "pw1"})
	d := pwDest(srv, "pw1", hostKeyLine(srv.hostKeys[0]))
	if err := remote(t, d).Delete(context.Background(), "no/such/file"); err != nil {
		t.Fatalf("Delete = %v", err)
	}
}

func TestSFTPErrorsNeverCarryPassword(t *testing.T) {
	srv := startSFTP(t, sftpOpts{password: "the-real-one"})
	const wrong = "wrong-pw-abc123"
	d := pwDest(srv, wrong, "")
	err := remote(t, d).Test(context.Background())
	if err == nil {
		t.Fatal("wrong password passed")
	}
	if strings.Contains(err.Error(), wrong) {
		t.Fatalf("error carries the password: %v", err)
	}
	d.HostKey = hostKeyLine(srv.hostKeys[0])
	if err := remote(t, d).Put(context.Background(), "a", strings.NewReader("x"), 1); err == nil || strings.Contains(err.Error(), wrong) {
		t.Fatalf("Put error = %v", err)
	}
}

func TestNewSFTPRefusesMissingCredentials(t *testing.T) {
	if _, err := NewSFTP(Destination{Type: TypeSFTP, SFTP: SFTPConfig{Host: "h", Port: 22, User: "u", Auth: AuthPassword}}); err == nil {
		t.Error("password auth without a password")
	}
	if _, err := NewSFTP(Destination{Type: TypeSFTP, SFTP: SFTPConfig{Host: "h", Port: 22, User: "u", Auth: AuthKey}}); err == nil {
		t.Error("key auth without a key")
	}
}
