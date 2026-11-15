package backupdest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// dialTimeout bounds the TCP connect; everything after it is bounded by the
// caller's context, enforced by closing the connection.
const dialTimeout = 30 * time.Second

// cleanupTimeout bounds removing a failed upload's .part.
const cleanupTimeout = time.Minute

// HostKeyRecorder is implemented by a Remote whose Test may meet a host key
// for the first time.
type HostKeyRecorder interface {
	// SeenHostKey is the authorized_keys line of the host key the last Test
	// accepted for the first time, or "" if none is new.
	SeenHostKey() string
}

// HostKeyLabel shows a stored host key line as "<type> SHA256:…", or "" for
// no key or an unreadable one.
func HostKeyLabel(line string) string {
	if line == "" {
		return ""
	}
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return ""
	}
	return k.Type() + " " + ssh.FingerprintSHA256(k)
}

type sftpRemote struct {
	d        Destination
	signer   ssh.Signer // key auth
	recorded ssh.PublicKey
	seen     string
	// pending is the unrecorded host key the last connection was shown; Test
	// promotes it to seen once it fully succeeds.
	pending ssh.PublicKey
}

// NewSFTP prepares a client for an SFTP destination. It sends nothing: every
// Put, Delete and Test opens its own connection.
func NewSFTP(d Destination) (Remote, error) {
	r := &sftpRemote{d: d}
	switch d.SFTP.Auth {
	case AuthKey:
		if d.PrivateKey == "" {
			return nil, errors.New("this SFTP destination has no SSH key: choose one")
		}
		s, err := ssh.ParsePrivateKey([]byte(d.PrivateKey))
		if err != nil {
			return nil, errors.New("the SSH key could not be read")
		}
		r.signer = s
	case AuthPassword:
		if d.Secret == "" {
			return nil, errors.New("this SFTP destination has no password: enter it")
		}
	default:
		return nil, errors.New("this SFTP destination has no login method")
	}
	if d.HostKey != "" {
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(d.HostKey))
		if err != nil {
			return nil, errors.New("the recorded host key could not be read: use Reset host key and Test again")
		}
		r.recorded = k
	}
	return r, nil
}

func (r *sftpRemote) SeenHostKey() string { return r.seen }

// hostKeyAlgorithms are the host key algorithms to ask for once k is
// recorded: the server may hold several key types and must present that one.
func hostKeyAlgorithms(k ssh.PublicKey) []string {
	if k.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{k.Type()}
}

func (r *sftpRemote) fail(err error) error { return errors.New(scrub(err.Error(), r.d.Secret)) }

// conn is one open SFTP session.
type conn struct {
	*sftp.Client
	net net.Conn
}

// connect opens one SSH connection and its SFTP session. Closing the
// connection when ctx ends is what enforces every timeout, because neither
// x/crypto/ssh nor pkg/sftp looks at ctx.
func (r *sftpRemote) connect(ctx context.Context) (*conn, func(), error) {
	var presented ssh.PublicKey
	changed := false
	cfg := &ssh.ClientConfig{
		User: r.d.SFTP.User,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if r.recorded != nil {
				if !bytes.Equal(key.Marshal(), r.recorded.Marshal()) {
					changed = true
					return ErrHostKeyChanged
				}
				return nil
			}
			presented = key
			return nil
		},
	}
	if r.recorded != nil {
		cfg.HostKeyAlgorithms = hostKeyAlgorithms(r.recorded)
	}
	if r.signer != nil {
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(r.signer)}
	} else {
		pw := r.d.Secret
		cfg.Auth = []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}
	}
	addr := net.JoinHostPort(r.d.SFTP.Host, strconv.Itoa(r.d.SFTP.Port))
	nc, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, r.dialErr(ctx, err)
	}
	stop := context.AfterFunc(ctx, func() { nc.Close() })
	closeAll := func(cl ...io.Closer) func() {
		return func() {
			for _, c := range cl {
				_ = c.Close()
			}
			stop()
			nc.Close()
		}
	}
	sc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	if err != nil {
		closeAll()()
		if changed {
			return nil, nil, ErrHostKeyChanged
		}
		return nil, nil, r.dialErr(ctx, err)
	}
	sshClient := ssh.NewClient(sc, chans, reqs)
	client, err := sftp.NewClient(sshClient, sftp.UseConcurrentWrites(true))
	if err != nil {
		closeAll(sshClient)()
		return nil, nil, r.dialErr(ctx, err)
	}
	if r.recorded == nil && presented != nil {
		r.pending = presented
	}
	return &conn{Client: client, net: nc}, closeAll(client, sshClient), nil
}

// dialErr turns a connection error into the one the caller sees: the
// context's when it is what cut the connection, else the scrubbed text.
func (r *sftpRemote) dialErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.fail(err)
}

func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// renameInto moves from over to. With posix it uses posix-rename, which
// replaces an existing target; without it the target is removed first.
func renameInto(c *sftp.Client, from, to string, posix bool) error {
	if posix {
		return c.PosixRename(from, to)
	}
	if err := c.Remove(to); err != nil && !isMissing(err) {
		return err
	}
	return c.Rename(from, to)
}

func (r *sftpRemote) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	if size < 0 {
		return errors.New("upload needs the file size")
	}
	if r.recorded == nil {
		return ErrNoHostKey
	}
	c, closeConn, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeConn()
	part := key + ".part"
	done := false
	defer func() {
		if !done {
			r.removePart(ctx, c, part)
		}
	}()
	if dir := path.Dir(key); dir != "." && dir != "/" {
		if err := c.MkdirAll(dir); err != nil {
			return r.dialErr(ctx, err)
		}
	}
	f, err := c.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return r.dialErr(ctx, err)
	}
	_, err = f.ReadFrom(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return r.dialErr(ctx, err)
	}
	st, err := c.Stat(part)
	if err != nil {
		return r.dialErr(ctx, err)
	}
	if st.Size() != size {
		return fmt.Errorf("the file is %d bytes on the server, expected %d", st.Size(), size)
	}
	_, posix := c.HasExtension("posix-rename@openssh.com")
	if err := renameInto(c.Client, part, key, posix); err != nil {
		return r.dialErr(ctx, err)
	}
	done = true
	return nil
}

// removePart removes a failed upload's .part: over the same connection when
// it still works, else over a fresh one. Either way within cleanupTimeout.
func (r *sftpRemote) removePart(ctx context.Context, c *conn, part string) {
	if ctx.Err() == nil {
		t := time.AfterFunc(cleanupTimeout, func() { c.net.Close() })
		err := c.Remove(part)
		alive := t.Stop()
		if alive && (err == nil || isMissing(err)) {
			return
		}
	}
	fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	c2, closeConn, err := r.connect(fresh)
	if err != nil {
		return
	}
	defer closeConn()
	_ = c2.Remove(part)
}

func (r *sftpRemote) Delete(ctx context.Context, key string) error {
	if r.recorded == nil {
		return ErrNoHostKey
	}
	c, closeConn, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeConn()
	if err := c.Remove(key); err != nil && !isMissing(err) {
		return r.dialErr(ctx, err)
	}
	return nil
}

func (r *sftpRemote) Test(ctx context.Context) (err error) {
	r.seen, r.pending = "", nil
	c, closeConn, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeConn()
	base := r.d.SFTP.Path
	if base != "" {
		if err := c.MkdirAll(base); err != nil {
			return r.dialErr(ctx, err)
		}
	}
	var tag [8]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return err
	}
	name := path.Join(base, ".relist-test-"+hex.EncodeToString(tag[:]))
	want := []byte("relist backup destination test " + hex.EncodeToString(tag[:]))
	f, err := c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return r.dialErr(ctx, err)
	}
	// Whatever happens next, do not leave the test file behind.
	defer func() { _ = c.Remove(name) }()
	_, err = f.Write(want)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return r.dialErr(ctx, err)
	}
	rf, err := c.Open(name)
	if err != nil {
		return r.dialErr(ctx, err)
	}
	defer rf.Close()
	got, err := io.ReadAll(rf)
	if err != nil {
		return r.dialErr(ctx, err)
	}
	if !bytes.Equal(got, want) {
		return errors.New("the file read back is not what was written")
	}
	if r.pending != nil {
		r.seen = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(r.pending)))
	}
	return nil
}
