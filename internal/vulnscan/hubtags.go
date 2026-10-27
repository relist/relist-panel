package vulnscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// youngTag is how old a tag must be before it stops carrying a warning: a
// compromised release tends to be found and withdrawn within days.
const youngTag = 14 * 24 * time.Hour

var semverTag = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

var errNotHub = errors.New("Recent tags can only be listed for Docker Hub images")

type hubTag struct {
	Name     string
	Digest   string
	PushedAt time.Time
}

type ScannerTag struct {
	Tag      string    `json:"tag"`
	Digest   string    `json:"digest"`
	Ref      string    `json:"ref"`
	PushedAt time.Time `json:"pushedAt"`
	AgeDays  int       `json:"ageDays"`
	Young    bool      `json:"young"`
}

// pickTags keeps release tags, newest first: those pushed in the last 14
// days, plus the newest one older than that, so a choice past the warning
// window is always offered.
func pickTags(raw []hubTag, now time.Time) []ScannerTag {
	releases := []hubTag{}
	for _, t := range raw {
		if semverTag.MatchString(t.Name) {
			releases = append(releases, t)
		}
	}
	sort.SliceStable(releases, func(i, j int) bool { return releases[i].PushedAt.After(releases[j].PushedAt) })
	out := []ScannerTag{}
	for _, t := range releases {
		age := now.Sub(t.PushedAt)
		young := age < youngTag
		out = append(out, ScannerTag{Tag: t.Name, Digest: t.Digest, PushedAt: t.PushedAt, AgeDays: int(age.Hours() / 24), Young: young})
		if !young {
			break
		}
	}
	return out
}

// hubRepository is the Docker Hub namespace/name of ref, or errNotHub for
// an image on another registry.
func hubRepository(ref string) (string, error) {
	name, _, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	parts := strings.Split(name, "/")
	if len(parts) > 1 {
		switch first := parts[0]; {
		case first == "docker.io" || first == "index.docker.io" || first == "registry-1.docker.io":
			parts = parts[1:]
		case strings.ContainsAny(first, ".:") || first == "localhost":
			return "", errNotHub
		}
	}
	if len(parts) == 1 {
		parts = []string{"library", parts[0]}
	}
	if len(parts) != 2 {
		return "", errNotHub
	}
	return parts[0] + "/" + parts[1], nil
}

// WithHub points the tag list at another Docker Hub API, for tests.
func (s *Service) WithHub(baseURL string, client *http.Client) *Service {
	s.hubBase, s.hubClient = baseURL, client
	return s
}

func (s *Service) scannerTags(ctx context.Context, ref string) ([]ScannerTag, error) {
	repo, err := hubRepository(ref)
	if err != nil {
		return nil, err
	}
	u := s.hubBase + "/v2/repositories/" + repo + "/tags?" + url.Values{"page_size": {"100"}, "ordering": {"last_updated"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.hubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Docker Hub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Docker Hub answered %s", resp.Status)
	}
	var page struct {
		Results []struct {
			Name          string    `json:"name"`
			Digest        string    `json:"digest"`
			TagLastPushed time.Time `json:"tag_last_pushed"`
			LastUpdated   time.Time `json:"last_updated"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&page); err != nil {
		return nil, fmt.Errorf("Docker Hub: %w", err)
	}
	raw := make([]hubTag, 0, len(page.Results))
	for _, r := range page.Results {
		pushed := r.TagLastPushed
		if pushed.IsZero() {
			pushed = r.LastUpdated
		}
		raw = append(raw, hubTag{Name: r.Name, Digest: r.Digest, PushedAt: pushed})
	}
	tags := pickTags(raw, s.d.Now())
	display := strings.TrimPrefix(repo, "library/")
	for i := range tags {
		tags[i].Ref = display + ":" + tags[i].Tag + "@" + tags[i].Digest
	}
	return tags, nil
}
