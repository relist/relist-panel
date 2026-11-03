package builders

import (
	"context"
	"regexp"
	"strings"
)

var sourceMetadataRef = regexp.MustCompile(`failed to resolve source metadata for (\S+?):\s`)

// FailureHint explains a build that failed on Builder id in the operator's
// terms, or returns "" when it has nothing to add: the Builder ran out of
// memory, or the build wanted an image that exists only on this host, which
// a Builder cannot see.
func (s *Service) FailureHint(ctx context.Context, id int64, buildErr error) string {
	if id == DockerDefault || buildErr == nil {
		return ""
	}
	b, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return ""
	}
	msg := buildErr.Error()
	if strings.Contains(msg, "cannot allocate memory") || strings.Contains(msg, "exit code: 137") {
		return "The build ran out of memory on Builder " + b.Name + " (" + memoryText(b.MemoryMB) + "). Raise its memory or pick a larger Builder for this Compose."
	}
	if m := sourceMetadataRef.FindStringSubmatch(msg); m != nil {
		if exists, err := s.d.Engine.ImageExists(ctx, m[1]); err == nil && exists {
			image := strings.TrimPrefix(m[1], "docker.io/library/")
			return image + " exists on this host, but Builders cannot use local images. Pick Docker default for this Compose, or push the image to a registry."
		}
	}
	return ""
}
