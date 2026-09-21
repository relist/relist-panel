// Package slug turns names into lowercase tokens safe for deploy identities.
package slug

import "strings"

// Slugify turns a name into a lowercase `[a-z0-9-]` token safe for both a
// filesystem path segment and a `docker compose -p` project name.
func Slugify(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if slug == "" {
		return "x"
	}
	return slug
}

// Conflicts reports whether candidate's slug matches any sibling's slug —
// the "My App" vs "my-app" collision that NOCASE name uniqueness lets through.
func Conflicts[T any](candidate string, siblings []T, nameOf func(T) string) bool {
	candidateSlug := Slugify(candidate)
	for _, sibling := range siblings {
		if Slugify(nameOf(sibling)) == candidateSlug {
			return true
		}
	}
	return false
}
