package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnsafeCheckout refuses a checkout that tracks a symlink resolving outside
// src/. Compose paths under ./src are host paths for the Docker daemon, so such
// a link would hand the repository's author files from the host.
var ErrUnsafeCheckout = errors.New("unsafe symlink in checkout")

// Ref is the commit a Fetch left checked out in src/.
type Ref struct{ SHA, Subject string }

// Branch is one branch head on the remote.
type Branch struct{ Name, SHA string }

// Git runs the git binary against a Source with its Deploy key.
type Git struct {
	Bin string // "" means "git"
}

func (g Git) bin() string {
	if g.Bin == "" {
		return "git"
	}
	return g.Bin
}

// sshCommand is the GIT_SSH_COMMAND that offers only the Deploy key and
// remembers host keys in the Compose's own known_hosts.
func sshCommand(keyPath, knownHosts string) string {
	return fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%s",
		shellQuote(keyPath), shellQuote(knownHosts))
}

// shellQuote leaves plain paths alone and single-quotes the rest, because git
// splits GIT_SSH_COMMAND with a shell.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:@%=,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshEnv writes the Deploy key to a 0600 temp file and returns the git
// environment that uses it, plus a cleanup that removes the file.
func sshEnv(src Source, composeDir string) ([]string, func(), error) {
	sshDir := filepath.Join(composeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create .ssh: %w", err)
	}
	f, err := os.CreateTemp("", "relist-key-*")
	if err != nil {
		return nil, nil, fmt.Errorf("write deploy key: %w", err)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	key := src.PrivateKey
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}
	if _, err := f.WriteString(key); err != nil {
		_ = f.Close()
		cleanup()
		return nil, nil, fmt.Errorf("write deploy key: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write deploy key: %w", err)
	}
	env := append(os.Environ(),
		"GIT_SSH_COMMAND="+sshCommand(f.Name(), filepath.Join(sshDir, "known_hosts")),
		"GIT_TERMINAL_PROMPT=0",
	)
	return env, cleanup, nil
}

func (g Git) run(ctx context.Context, dir string, env []string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, g.bin(), args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A killed git can leave a grandchild (ssh, a hook) holding the pipes;
	// without a WaitDelay, Run would block on them and hang Cancel and Delete.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

// Fetch makes the Compose's src/ a clean shallow checkout of the Source's
// branch, limited to its SparsePaths when it has any, and returns the commit
// it holds. Progress is written to out.
func (g Git) Fetch(ctx context.Context, src Source, composeDir string, out io.Writer) (Ref, error) {
	if src.Branch == "" && src.PinnedSHA == "" {
		return Ref{}, errors.New("the Source has no branch")
	}
	if err := EnsureOwnedSrc(composeDir); err != nil {
		return Ref{}, err
	}
	srcDir := filepath.Join(composeDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return Ref{}, fmt.Errorf("create src: %w", err)
	}
	if err := ensureMarker(srcDir); err != nil {
		return Ref{}, err
	}

	env, cleanup, err := sshEnv(src, composeDir)
	if err != nil {
		return Ref{}, err
	}
	defer cleanup()

	step := func(args ...string) error {
		return g.run(ctx, srcDir, env, out, out, args...)
	}
	if err := step("init"); err != nil {
		return Ref{}, err
	}
	if g.run(ctx, srcDir, env, io.Discard, io.Discard, "remote", "get-url", "origin") == nil {
		err = step("remote", "set-url", "origin", src.RepoURL)
	} else {
		err = step("remote", "add", "origin", src.RepoURL)
	}
	if err != nil {
		return Ref{}, err
	}
	revision := src.Branch
	if src.PinnedSHA != "" {
		revision = src.PinnedSHA
	}
	// With paths, only their files are downloaded: the fetch leaves out every
	// file's contents and checkout fetches the ones the sparse tree needs.
	fetch := []string{"fetch", "--depth", "1"}
	if len(src.SparsePaths) > 0 {
		if err := step(append([]string{"sparse-checkout", "set", "--cone", "--"}, src.SparsePaths...)...); err != nil {
			return Ref{}, err
		}
		fetch = append(fetch, "--filter=blob:none")
	} else if err := step("sparse-checkout", "disable"); err != nil {
		return Ref{}, err
	}
	if err := step(append(fetch, "origin", revision)...); err != nil {
		return Ref{}, err
	}
	// Cone mode quietly checks out nothing for a missing path or a file, and
	// the build would then fail far from the cause.
	for _, p := range src.SparsePaths {
		var kind bytes.Buffer
		if err := g.run(ctx, srcDir, env, &kind, io.Discard, "cat-file", "-t", "FETCH_HEAD:"+p); err != nil || strings.TrimSpace(kind.String()) != "tree" {
			return Ref{}, fmt.Errorf("path %q is not a directory in the repository", p)
		}
	}
	if err := step("checkout", "--force", "FETCH_HEAD"); err != nil {
		return Ref{}, err
	}
	if err := step("clean", "-ffdx", "-e", MarkerName); err != nil {
		return Ref{}, err
	}
	// The repository is untrusted and may track something named like the marker.
	if err := ensureMarker(srcDir); err != nil {
		return Ref{}, err
	}
	if src.Submodules {
		args := []string{}
		if strings.HasPrefix(src.RepoURL, "file://") {
			args = append(args, "-c", "protocol.file.allow=always")
		}
		args = append(args, "submodule", "update", "--init", "--recursive", "--depth", "1")
		// git would otherwise also clone submodules outside the paths.
		if len(src.SparsePaths) > 0 {
			args = append(append(args, "--"), src.SparsePaths...)
		}
		if err := step(args...); err != nil {
			return Ref{}, err
		}
	}

	if err := checkSymlinks(srcDir); err != nil {
		return Ref{}, err
	}

	var logOut bytes.Buffer
	if err := g.run(ctx, srcDir, env, &logOut, out, "log", "-1", "--format=%H%n%s"); err != nil {
		return Ref{}, err
	}
	sha, subject, _ := strings.Cut(strings.TrimRight(logOut.String(), "\n"), "\n")
	if src.PinnedSHA != "" && sha != src.PinnedSHA {
		return Ref{}, errors.New("fetched commit differs from the queued revision")
	}
	return Ref{SHA: sha, Subject: subject}, nil
}

// checkSymlinks resolves every symlink in the working tree the way the kernel
// will, through os.Root so a link that leaves srcDir errors instead of being
// followed. Escaping links are removed so they never outlive the refusal; one
// that dangles inside the tree is fine.
func checkSymlinks(srcDir string) error {
	root, err := os.OpenRoot(srcDir)
	if err != nil {
		return err
	}
	defer root.Close()
	var escaping []string
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		if _, err := root.Stat(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			escaping = append(escaping, name)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("check symlinks: %w", err)
	}
	if len(escaping) == 0 {
		return nil
	}
	for _, name := range escaping {
		root.Remove(name)
	}
	return fmt.Errorf("%w: %q does not resolve inside the repository", ErrUnsafeCheckout, escaping[0])
}

// ResolveRevision reads only the configured remote branch, with a bounded
// context. No working tree or database transaction is required.
func (g Git) ResolveRevision(ctx context.Context, src Source, composeDir string) (string, error) {
	if src.Branch == "" {
		return "", errors.New("the Source has no branch")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	env, cleanup, err := sshEnv(src, composeDir)
	if err != nil {
		return "", err
	}
	defer cleanup()
	var out bytes.Buffer
	if err := g.run(ctx, composeDir, env, &out, io.Discard, "ls-remote", "--heads", "--", src.RepoURL, "refs/heads/"+src.Branch); err != nil {
		return "", err
	}
	for _, line := range strings.Split(out.String(), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && ref == "refs/heads/"+src.Branch && validSHA(sha) {
			return sha, nil
		}
	}
	return "", errors.New("the configured Source branch was not found")
}

func validSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	return strings.Trim(sha, "0123456789abcdef") == ""
}

// ListBranches lists the remote's branch heads. It needs no working tree.
func (g Git) ListBranches(ctx context.Context, src Source, composeDir string) ([]Branch, error) {
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create compose directory: %w", err)
	}
	env, cleanup, err := sshEnv(src, composeDir)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var stdout, stderr bytes.Buffer
	if err := g.run(ctx, composeDir, env, &stdout, &stderr, "ls-remote", "--heads", "--", src.RepoURL); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(stdout.String(), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		name, isHead := strings.CutPrefix(ref, "refs/heads/")
		if ok && isHead {
			branches = append(branches, Branch{Name: name, SHA: sha})
		}
	}
	return branches, nil
}

// ensureMarker makes srcDir/.relist-source a regular file without ever opening
// it through a link: anything else there is removed and recreated exclusively.
func ensureMarker(srcDir string) error {
	path := filepath.Join(srcDir, MarkerName)
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.Mode().IsRegular():
		return nil
	case err == nil:
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("mark src: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("mark src: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("mark src: %w", err)
	}
	return f.Close()
}
