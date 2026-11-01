// Command relist-release builds, signs and checks an Update manifest. The
// release workflow runs it; the owner runs keygen and verify by hand
// (ADR-0024).
//
//	relist-release keygen
//	relist-release manifest --version 1.4.0 --digest sha256:… [--out update-manifest.json]
//	relist-release sign     [--manifest update-manifest.json] [--out update-manifest.json.sig]
//	relist-release verify   --key <base64 public key> [--manifest …] [--sig …]
//
// sign reads the base64 private key from RELIST_RELEASE_KEY, never from a flag,
// so it stays out of process listings and logs.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"relist-panel/internal/panelupdate"
)

const (
	defaultRepo     = "ghcr.io/relist/relist-panel"
	defaultNotesFmt = "https://github.com/relist/relist-panel/releases/tag/v%s"
	keyEnv          = "RELIST_RELEASE_KEY"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Getenv, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, "relist-release:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, getenv func(string) string, now func() time.Time) error {
	if len(args) == 0 {
		return errors.New("usage: relist-release keygen|manifest|sign|verify [flags]")
	}
	switch args[0] {
	case "keygen":
		return keygen(stdout)
	case "manifest":
		return manifest(args[1:], now)
	case "sign":
		return sign(args[1:], getenv)
	case "verify":
		return verify(args[1:], stdout)
	}
	return fmt.Errorf("unknown subcommand %q", args[0])
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func keygen(stdout io.Writer) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "private (secret %s, keep offline): %s\n", keyEnv, base64.StdEncoding.EncodeToString(priv))
	fmt.Fprintf(stdout, "public (releasePublicKey in internal/panelupdate/key.go): %s\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

func manifest(args []string, now func() time.Time) error {
	fs := newFlags("manifest")
	version := fs.String("version", "", "release version without the v, e.g. 1.4.0")
	digest := fs.String("digest", "", "the image index digest, sha256:…")
	repo := fs.String("repo", defaultRepo, "image repository")
	notes := fs.String("notes-url", "", "release notes URL (default: the tag's GitHub Release)")
	released := fs.String("released-at", "", "RFC 3339 time (default: now)")
	compose := fs.String("compose", "docker-compose.yml", "the version's Install file")
	invariants := fs.String("invariants", "release/invariants.json", "")
	steps := fs.String("manual-steps", "release/manual-steps.json", "")
	out := fs.String("out", "update-manifest.json", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" || *digest == "" {
		return errors.New("manifest needs --version and --digest")
	}
	if !strings.HasPrefix(*digest, "sha256:") {
		return fmt.Errorf("the digest %q is not a sha256 digest", *digest)
	}
	at := now().UTC()
	if *released != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, *released); err != nil {
			return fmt.Errorf("--released-at: %w", err)
		}
	}
	installFile, err := os.ReadFile(*compose)
	if err != nil {
		return err
	}
	m := panelupdate.Manifest{
		Schema:      1,
		Version:     *version,
		ReleasedAt:  at,
		Image:       *repo + "@" + *digest,
		NotesURL:    *notes,
		InstallFile: string(installFile),
		Invariants:  []panelupdate.Invariant{},
		ManualSteps: []panelupdate.ManualStep{},
	}
	if m.NotesURL == "" {
		m.NotesURL = fmt.Sprintf(defaultNotesFmt, *version)
	}
	if err := readJSON(*invariants, &m.Invariants); err != nil {
		return err
	}
	if unknown := panelupdate.UnknownKinds(m.Invariants); len(unknown) > 0 {
		return fmt.Errorf("%s: unknown invariant kinds %s (known: %s)",
			*invariants, strings.Join(unknown, ", "), strings.Join(panelupdate.InvariantKinds(), ", "))
	}
	if err := readJSON(*steps, &m.ManualSteps); err != nil {
		return err
	}
	for _, s := range m.ManualSteps {
		if s.ID == "" || s.Text == "" {
			return fmt.Errorf("%s: every manual step needs an id and text", *steps)
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// The signature covers these exact bytes, so write them once, as is.
	return os.WriteFile(*out, append(data, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func sign(args []string, getenv func(string) string) error {
	fs := newFlags("sign")
	in := fs.String("manifest", "update-manifest.json", "")
	out := fs.String("out", "", "signature file (default: <manifest>.sig)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		*out = *in + ".sig"
	}
	priv, err := decodePrivate(getenv(keyEnv))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, ed25519.Sign(priv, data), 0o644)
}

func decodePrivate(s string) (ed25519.PrivateKey, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is not set", keyEnv)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is not a base64 ed25519 private key", keyEnv)
	}
	return ed25519.PrivateKey(b), nil
}

// verify runs the panel's own ParseManifest, so a manifest that passes here
// is one a panel accepts.
func verify(args []string, stdout io.Writer) error {
	fs := newFlags("verify")
	in := fs.String("manifest", "update-manifest.json", "")
	sigFile := fs.String("sig", "", "signature file (default: <manifest>.sig)")
	keyArg := fs.String("key", "", "base64 public key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sigFile == "" {
		*sigFile = *in + ".sig"
	}
	key, err := panelupdate.DecodeKey(*keyArg)
	if err != nil {
		return fmt.Errorf("--key: %w", err)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigFile)
	if err != nil {
		return err
	}
	m, err := panelupdate.ParseManifest(data, sig, key)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ok: version %s, image %s, %d invariants, %d manual steps\n",
		m.Version, m.Image, len(m.Invariants), len(m.ManualSteps))
	return nil
}
