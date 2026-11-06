package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// InternalBase is how the panel reaches the registry: over relist-network,
// never through the public hostname, so Repositories works without hairpin
// NAT (ADR-0033).
const InternalBase = "http://relist-registry:5000"

// Client reads and deletes tags through the registry HTTP API with the
// system login.
type Client struct {
	Base         string
	User, Secret string
	HTTP         *http.Client
}

// TagInfo is one tag: the digest it points at, the image's compressed size
// (config plus layers) and its config's created time.
type TagInfo struct {
	Tag     string
	Digest  string
	Size    int64
	Created time.Time
}

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json"

var nextLink = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do sends one request; a status outside 2xx is an error naming it.
func (c *Client) do(ctx context.Context, method, path string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.User, c.Secret)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("registry %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path, accept string, out any) (http.Header, error) {
	resp, err := c.do(ctx, http.MethodGet, path, accept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return nil, fmt.Errorf("registry GET %s: %w", path, err)
	}
	return resp.Header, nil
}

// paged follows Link: rel="next" from first, collecting each page's list.
func (c *Client) paged(ctx context.Context, first string, page func(raw json.RawMessage) ([]string, error)) ([]string, error) {
	out := []string{}
	for path := first; path != ""; {
		var raw json.RawMessage
		header, err := c.getJSON(ctx, path, "", &raw)
		if err != nil {
			return nil, err
		}
		items, err := page(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		path = ""
		if m := nextLink.FindStringSubmatch(header.Get("Link")); m != nil {
			if u, err := url.Parse(m[1]); err == nil {
				path = u.RequestURI()
			}
		}
	}
	return out, nil
}

func (c *Client) Repositories(ctx context.Context) ([]string, error) {
	return c.paged(ctx, "/v2/_catalog?n=1000", func(raw json.RawMessage) ([]string, error) {
		var body struct {
			Repositories []string `json:"repositories"`
		}
		return body.Repositories, json.Unmarshal(raw, &body)
	})
}

func (c *Client) Tags(ctx context.Context, repo string) ([]string, error) {
	return c.paged(ctx, "/v2/"+repo+"/tags/list?n=1000", func(raw json.RawMessage) ([]string, error) {
		var body struct {
			Tags []string `json:"tags"`
		}
		return body.Tags, json.Unmarshal(raw, &body)
	})
}

type manifest struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
	Config struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
}

// TagInfo reads a tag. For an index (a buildx push), size and created come
// from the first image that is not an attestation (platform unknown/unknown);
// the digest is the index's, which is what a delete takes.
func (c *Client) TagInfo(ctx context.Context, repo, tag string) (TagInfo, error) {
	var m manifest
	header, err := c.getJSON(ctx, "/v2/"+repo+"/manifests/"+tag, manifestAccept, &m)
	if err != nil {
		return TagInfo{}, err
	}
	info := TagInfo{Tag: tag, Digest: header.Get("Docker-Content-Digest")}
	if len(m.Manifests) > 0 {
		child := ""
		for _, d := range m.Manifests {
			if d.Platform.OS != "unknown" || d.Platform.Architecture != "unknown" {
				child = d.Digest
				break
			}
		}
		if child == "" {
			return info, nil
		}
		m = manifest{}
		if _, err := c.getJSON(ctx, "/v2/"+repo+"/manifests/"+child, manifestAccept, &m); err != nil {
			return TagInfo{}, err
		}
	}
	info.Size = m.Config.Size
	for _, l := range m.Layers {
		info.Size += l.Size
	}
	if m.Config.Digest != "" {
		var cfg struct {
			Created time.Time `json:"created"`
		}
		if _, err := c.getJSON(ctx, "/v2/"+repo+"/blobs/"+m.Config.Digest, "", &cfg); err == nil {
			info.Created = cfg.Created
		}
	}
	return info, nil
}

// Delete removes a manifest by digest, and with it every tag pointing at it.
func (c *Client) Delete(ctx context.Context, repo, digest string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v2/"+repo+"/manifests/"+digest, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
