package sources

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// GenerateKey makes a Deploy key: a fresh ed25519 pair. privatePEM is the
// OpenSSH private key for ssh -i; authorizedKey is the public half on one
// line, without a trailing newline, ending in " <comment>", ready to paste
// into the repository host.
func GenerateKey(comment string) (privatePEM, authorizedKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate deploy key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", fmt.Errorf("marshal deploy key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("deploy key public half: %w", err)
	}
	line := string(ssh.MarshalAuthorizedKey(sshPub))
	line = line[:len(line)-1] // MarshalAuthorizedKey ends with a newline
	return string(pem.EncodeToMemory(block)), line + " " + comment, nil
}
