package sources

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateKey(t *testing.T) {
	priv, pub, err := GenerateKey("relist-c42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParsePrivateKey([]byte(priv)); err != nil {
		t.Fatalf("private key does not parse: %v", err)
	}
	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		t.Fatalf("authorized key does not parse: %v", err)
	}
	if key.Type() != "ssh-ed25519" {
		t.Errorf("type = %q", key.Type())
	}
	if comment != "relist-c42" {
		t.Errorf("comment = %q", comment)
	}
	if !strings.HasSuffix(strings.TrimSpace(pub), " relist-c42") {
		t.Errorf("authorized key = %q", pub)
	}
	priv2, pub2, err := GenerateKey("relist-c42")
	if err != nil {
		t.Fatal(err)
	}
	if priv == priv2 || pub == pub2 {
		t.Error("two calls gave the same key")
	}
}
