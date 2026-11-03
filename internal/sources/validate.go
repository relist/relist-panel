package sources

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	maxRepoURLLen = 512
	maxBranchLen  = 255
)

// Only SSH remotes are accepted, because a Deploy key only authenticates over
// SSH: the scp-like `user@host:path` form and `ssh://user@host[:port]/path`.
// Neither can begin with `-`, so the value is never read as an ssh or git
// option, and neither can hold whitespace.
var (
	scpRepoURL = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]*@[A-Za-z0-9.-]+:[^\s:][^\s]*$`)
	sshRepoURL = regexp.MustCompile(`^ssh://[A-Za-z0-9._][A-Za-z0-9._-]*@[A-Za-z0-9.-]+(:[0-9]{1,5})?/[^\s]+$`)
)

// ValidateRepoURL trims v and checks it is an SSH repository URL.
func ValidateRepoURL(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "", errors.New("repoUrl is required")
	case len(v) > maxRepoURLLen:
		return "", fmt.Errorf("repoUrl must be at most %d characters", maxRepoURLLen)
	case !scpRepoURL.MatchString(v) && !sshRepoURL.MatchString(v):
		return "", errors.New("repoUrl must be an SSH URL like git@host:owner/repo.git")
	}
	return v, nil
}

// ValidateBranch checks v against git's ref-name rules and refuses a leading
// `-`, so it is safe as a git argument. An empty branch is allowed and means
// the remote's default branch.
func ValidateBranch(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("branch %q is not a valid branch name: %s", v, why)
	}
	switch {
	case len(v) > maxBranchLen:
		return "", fmt.Errorf("branch must be at most %d characters", maxBranchLen)
	case v == "@":
		return bad("it is `@`")
	case strings.HasPrefix(v, "-"):
		return bad("it starts with `-`")
	case strings.HasSuffix(v, "/"), strings.HasSuffix(v, "."):
		return bad("it ends with `/` or `.`")
	case strings.Contains(v, ".."), strings.Contains(v, "//"), strings.Contains(v, "@{"):
		return bad("it contains `..`, `//` or `@{`")
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return bad("it contains a forbidden character")
		}
	}
	for _, part := range strings.Split(v, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return bad("a path component starts with `.` or ends with `.lock`")
		}
	}
	return v, nil
}

const (
	maxSparsePaths   = 32
	maxSparsePathLen = 255
)

// ValidateSparsePaths cleans the directories a Git Source checks out and
// drops duplicates. Each is relative to the repository root and stays inside
// it; none can be read as a git option or a sparse-checkout pattern. No paths
// means the whole repository.
func ValidateSparsePaths(in []string) ([]string, error) {
	if len(in) > maxSparsePaths {
		return nil, fmt.Errorf("at most %d paths", maxSparsePaths)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		raw := strings.TrimSpace(v)
		bad := func(why string) ([]string, error) {
			return nil, fmt.Errorf("path %q %s", raw, why)
		}
		if raw == "" {
			continue
		}
		if len(raw) > maxSparsePathLen {
			return nil, fmt.Errorf("a path must be at most %d characters", maxSparsePathLen)
		}
		if strings.HasPrefix(raw, "/") {
			return bad("must be relative to the repository root")
		}
		for _, r := range raw {
			if r < 0x20 || r == 0x7f || strings.ContainsRune(`*?[]\!#`, r) {
				return bad("contains a forbidden character")
			}
		}
		p := path.Clean(raw)
		if p == "." {
			return bad("is the repository root; leave paths empty to check out everything")
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." || part == ".git" {
				return bad("must stay inside the repository")
			}
		}
		if strings.HasPrefix(p, "-") {
			return bad("starts with `-`")
		}
		// git reads a leading `:` in a pathspec as magic such as `:(exclude)`.
		if strings.HasPrefix(p, ":") {
			return bad("starts with `:`")
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}
