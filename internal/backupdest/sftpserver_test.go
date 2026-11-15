package backupdest

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sftpOpts configures the in-process test server.
type sftpOpts struct {
	hostKeys  []ssh.Signer  // default: one ed25519 key
	clientKey ssh.PublicKey // accepted public key
	password  string        // accepted password
	// stall completes SSH and accepts the sftp subsystem, then never answers.
	stall bool
}

// testSFTP is a running in-process SFTP server over a temp directory.
type testSFTP struct {
	addr     string
	host     string
	port     int
	hostKeys []ssh.Signer
	root     string
	conns    atomic.Int32
}

func newSigner(t *testing.T, kind string) ssh.Signer {
	t.Helper()
	var key any
	var err error
	switch kind {
	case "rsa":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ecdsa":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		_, key, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startSFTP(t *testing.T, opts sftpOpts) *testSFTP {
	t.Helper()
	if len(opts.hostKeys) == 0 {
		opts.hostKeys = []ssh.Signer{newSigner(t, "ed25519")}
	}
	srv := &testSFTP{hostKeys: opts.hostKeys, root: t.TempDir()}
	cfg := &ssh.ServerConfig{}
	if opts.clientKey != nil {
		want := opts.clientKey.Marshal()
		cfg.PublicKeyCallback = func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if string(k.Marshal()) == string(want) {
				return nil, nil
			}
			return nil, io.EOF
		}
	}
	if opts.password != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == opts.password {
				return nil, nil
			}
			return nil, io.EOF
		}
	}
	for _, k := range opts.hostKeys {
		cfg.AddHostKey(k)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	srv.addr = l.Addr().String()
	host, port, _ := net.SplitHostPort(srv.addr)
	srv.host = host
	srv.port = atoiPort(port)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			srv.conns.Add(1)
			go srv.serve(c, cfg, opts)
		}
	}()
	return srv
}

func atoiPort(s string) (n int) {
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return
}

func (srv *testSFTP) serve(c net.Conn, cfg *ssh.ServerConfig, opts sftpOpts) {
	defer c.Close()
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close()
			for req := range creqs {
				ok := req.Type == "subsystem" && strings.HasSuffix(string(req.Payload), "sftp")
				_ = req.Reply(ok, nil)
				if !ok {
					continue
				}
				if opts.stall {
					_, _ = io.Copy(io.Discard, ch) // read, never answer
					return
				}
				s, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(srv.root))
				if err != nil {
					return
				}
				_ = s.Serve()
				return
			}
		}()
	}
}
