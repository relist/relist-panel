package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const setupUsage = "usage: relist-panel setup --dir PATH --domain HOST --image REF [--acme-email ADDRESS]"

// runSetup is `relist-panel setup`: it writes the Install file and a .env into
// an empty Install directory. The Install script runs it in a Container of the
// release image, so the files come from the same build as the panel.
func runSetup(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(stderr, setupUsage) }
	dir := flags.String("dir", "", "the Install directory, an absolute path that exists")
	domain := flags.String("domain", "", "the panel's host name")
	image := flags.String("image", "", "the panel image reference to run")
	acme := flags.String("acme-email", "", "the email Let's Encrypt writes to (optional)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 || *dir == "" || *domain == "" || *image == "" {
		fmt.Fprintln(stderr, setupUsage)
		return 2
	}
	if !filepath.IsAbs(*dir) {
		fmt.Fprintln(stderr, "setup: --dir must be an absolute path")
		return 2
	}
	for name, v := range map[string]string{"--domain": *domain, "--image": *image, "--acme-email": *acme} {
		if bad := strings.IndexFunc(v, unsafeEnvRune); bad >= 0 {
			fmt.Fprintf(stderr, "setup: %s has a character that does not belong in a .env value\n", name)
			return 2
		}
	}
	if info, err := os.Stat(*dir); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "setup: %s is not a directory that exists\n", *dir)
		return 1
	}
	composePath := filepath.Join(*dir, "docker-compose.yml")
	envPath := filepath.Join(*dir, ".env")
	for _, p := range []string{composePath, envPath} {
		if _, err := os.Lstat(p); err == nil {
			fmt.Fprintf(stderr, "setup: %s already exists; nothing was written\n", p)
			return 1
		}
	}
	env := "RELIST_DOMAIN=" + *domain + "\n" +
		"RELIST_ACME_EMAIL=" + *acme + "\n" +
		"RELIST_PANEL_IMAGE=" + *image + "\n" +
		"COMPOSE_PROJECT_NAME=relist-panel\n"
	if err := createNew(composePath, installFile, 0o644); err != nil {
		return setupFailed(stderr, composePath, err)
	}
	if err := createNew(envPath, []byte(env), 0o600); err != nil {
		_ = os.Remove(composePath)
		return setupFailed(stderr, envPath, err)
	}
	fmt.Fprintf(stdout, "Wrote %s and %s\n", composePath, envPath)
	return 0
}

func setupFailed(stderr io.Writer, path string, err error) int {
	if errors.Is(err, fs.ErrExist) {
		fmt.Fprintf(stderr, "setup: %s already exists; nothing was written\n", path)
	} else {
		fmt.Fprintf(stderr, "setup: write %s: %v\n", path, err)
	}
	return 1
}

// createNew writes a file that must not exist yet, with exactly mode.
func createNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Chmod(mode); err != nil { // the umask may have narrowed it
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// unsafeEnvRune is a character that would end a .env value early, start a
// comment, be interpolated by Compose or break the line.
func unsafeEnvRune(r rune) bool {
	return r <= ' ' || r == 0x7f || strings.ContainsRune("$\"'`#\\", r)
}
