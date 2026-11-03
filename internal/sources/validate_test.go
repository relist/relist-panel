package sources

import (
	"strings"
	"testing"
)

func TestValidateRepoURL(t *testing.T) {
	for _, ok := range []string{
		"git@github.com:acme/shop.git",
		"ssh://git@gitlab.example.com:2222/acme/shop.git",
		"git@gitea.local:a/b",
	} {
		got, err := ValidateRepoURL("  " + ok + " ")
		if err != nil || got != ok {
			t.Errorf("ValidateRepoURL(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{
		"https://github.com/acme/shop.git",
		"-oProxyCommand=x",
		"git@github.com:acme/shop.git x",
		"git@" + strings.Repeat("a", 509),
		"",
	} {
		if _, err := ValidateRepoURL(bad); err == nil {
			t.Errorf("ValidateRepoURL(%q) accepted", bad)
		}
	}
}

func TestValidateBranch(t *testing.T) {
	for _, ok := range []string{"", "main", "feature/checkout-v2"} {
		if got, err := ValidateBranch(ok); err != nil || got != ok {
			t.Errorf("ValidateBranch(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{
		"-x", "a..b", "a b", "a~1", "a^", "a:b", "a?", "a*", "a[", `a\b`,
		"a/", "a.lock", "a@{1}", "a//b", ".a", "a\x7f", "a\tb", "@", "a.",
		"x/.y", "x/a.lock", strings.Repeat("a", 256),
	} {
		if _, err := ValidateBranch(bad); err == nil {
			t.Errorf("ValidateBranch(%q) accepted", bad)
		}
	}
}

func TestValidateSparsePaths(t *testing.T) {
	got, err := ValidateSparsePaths([]string{" web/ ", "", "api", "./web", "docs/a b", "a/./b"})
	if err != nil || strings.Join(got, "|") != "web|api|docs/a b|a/b" {
		t.Fatalf("ValidateSparsePaths = %q, %v", got, err)
	}
	if got, err := ValidateSparsePaths(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ValidateSparsePaths(nil) = %#v, %v, want empty", got, err)
	}
	for _, bad := range []string{
		"/abs", "..", "a/../..", "a/../../b", ".", "./", "-x", ":(exclude)x", ":/x", ".git", "a/.git/b",
		"a*", "a?", "a[b]", `a\b`, "!a", "#a", "a\tb", "a\x7f", strings.Repeat("a", 256),
	} {
		if _, err := ValidateSparsePaths([]string{bad}); err == nil {
			t.Errorf("ValidateSparsePaths(%q) accepted", bad)
		}
	}
	if _, err := ValidateSparsePaths(make([]string, 33)); err == nil {
		t.Error("33 paths accepted")
	}
}
