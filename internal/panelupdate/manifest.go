// Package panelupdate checks for a newer panel release and, in later steps,
// applies it (ADR-0024).
package panelupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Manifest is a parsed Update manifest. Raw holds the exact signed bytes.
type Manifest struct {
	Schema      int          `json:"schema"`
	Version     string       `json:"version"`
	ReleasedAt  time.Time    `json:"released_at"`
	Image       string       `json:"image"`
	NotesURL    string       `json:"notes_url"`
	InstallFile string       `json:"install_file"`
	Invariants  []Invariant  `json:"invariants"`
	ManualSteps []ManualStep `json:"manual_steps"`

	Raw []byte `json:"-"`
}

// Invariant is one Install invariant. The kind is read here; the fields stay
// raw for the code that evaluates the kind.
type Invariant struct {
	Kind string
	Raw  json.RawMessage
}

func (i *Invariant) UnmarshalJSON(b []byte) error {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	if head.Kind == "" {
		return errors.New("an invariant has no kind")
	}
	i.Kind = head.Kind
	i.Raw = append(i.Raw[:0], b...)
	return nil
}

func (i Invariant) MarshalJSON() ([]byte, error) { return i.Raw, nil }

type ManualStep struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// supportedSchema is the only manifest layout this build reads.
const supportedSchema = 1

// ParseManifest checks sig, a raw ed25519 signature, over the exact bytes of
// data, then parses them. A manifest that fails either step is no manifest.
func ParseManifest(data, sig []byte, key ed25519.PublicKey) (*Manifest, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, data, sig) {
		return nil, errors.New("the manifest signature does not match")
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("the manifest is not valid: %w", err)
	}
	if m.Schema != supportedSchema {
		return nil, fmt.Errorf("the manifest schema %d is not supported", m.Schema)
	}
	if _, ok := parseVersion(m.Version); !ok {
		return nil, fmt.Errorf("the manifest version %q is not a release version", m.Version)
	}
	if m.Image == "" {
		return nil, errors.New("the manifest names no image")
	}
	m.Raw = append([]byte(nil), data...)
	return &m, nil
}

// DecodeKey reads a base64 ed25519 public key.
func DecodeKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64 ed25519 public key")
	}
	if err := checkKey(b); err != nil {
		return nil, err
	}
	return ed25519.PublicKey(b), nil
}
