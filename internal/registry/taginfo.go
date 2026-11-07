package registry

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/errgroup"
)

// infoCache holds tag info by repo@manifest-digest: a manifest never
// changes, so a later read only HEADs each tag for its digest. The Registry
// page and cleanup share it through the Service.
type infoCache struct {
	infoMu sync.Mutex
	infos  map[string]TagInfo
}

// maxCachedInfos bounds the tag info cache; past it the cache starts over.
const maxCachedInfos = 10000

// tagInfo reads a tag, from the cache when its manifest digest was read
// before.
func (s *Service) tagInfo(ctx context.Context, c RegistryAPI, repo, tag string) (TagInfo, error) {
	digest, err := c.Digest(ctx, repo, tag)
	if err != nil {
		return TagInfo{}, err
	}
	if digest == "" { // no Docker-Content-Digest: nothing to key the cache by
		return c.TagInfo(ctx, repo, tag)
	}
	key := repo + "@" + digest
	s.infoMu.Lock()
	info, ok := s.infos[key]
	s.infoMu.Unlock()
	if ok {
		info.Tag = tag
		return info, nil
	}
	if info, err = c.TagInfo(ctx, repo, tag); err != nil {
		return TagInfo{}, err
	}
	// A created time that could not be read (its config blob failed) is
	// read again next time instead of shown as unknown for good.
	if info.Created.IsZero() || info.Digest == "" {
		return info, nil
	}
	s.infoMu.Lock()
	if s.infos == nil || len(s.infos) >= maxCachedInfos {
		s.infos = map[string]TagInfo{}
	}
	s.infos[repo+"@"+info.Digest] = info
	s.infoMu.Unlock()
	return info, nil
}

// readTagInfos reads tags of repo, eight at a time, in tags' order. A tag
// whose manifest is gone (listed, then deleted) is left out, and said in
// logf when given. The Registry page and cleanup both read through it, so
// Kept and the cleanup preview see the same tags.
func readTagInfos(ctx context.Context, s *Service, reg RegistryAPI, repo string, tags []string, logf func(string, ...any)) ([]TagInfo, error) {
	out := make([]TagInfo, len(tags))
	gone := make([]bool, len(tags))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, tag := range tags {
		g.Go(func() error {
			info, err := s.tagInfo(ctx, reg, repo, tag)
			if errors.Is(err, ErrNotFound) {
				gone[i] = true
				return nil
			}
			out[i] = info
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	kept := out[:0]
	for i, info := range out {
		if !gone[i] {
			kept = append(kept, info)
		} else if logf != nil {
			logf("%s: skipped %s, whose manifest is gone", repo, tags[i])
		}
	}
	return kept, nil
}
