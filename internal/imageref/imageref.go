// Package imageref spells image references the way Docker lists a pulled
// image.
package imageref

import "strings"

// Pinned is how Docker lists an image pulled as ref: by its tag, when ref
// has one, and as repo@digest, when ref pins a digest. Neither is "busybox"
// alone, so that gives nothing.
func Pinned(ref string) []string {
	name, digest, _ := strings.Cut(ref, "@")
	refs := []string{}
	repo := name
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		repo = name[:i]
		refs = append(refs, name)
	}
	if digest != "" {
		refs = append(refs, repo+"@"+digest)
	}
	return refs
}
