package dockerexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"relist-panel/internal/images"
)

// Images implements images.Images over the Docker Engine API on the mounted
// socket — ADR-0006's direct API path, the same socket ContainerSource and
// TerminalExec use. Its client has no timeout: loading an archive streams for
// minutes, and the request's context is what ends it.
type Images struct {
	client *http.Client
}

// NewImages constructs an adapter over the socket at socketPath.
func NewImages(socketPath string) *Images {
	return &Images{client: newSocketClient(socketPath)}
}

var _ images.Images = (*Images)(nil)

// List reads GET /images/json and joins it with GET /containers/json?all=1 —
// stopped containers included — to mark each image InUse. An image with no tag
// (RepoTags null, or the older daemons' "<none>:<none>") gets an empty,
// non-nil Tags slice.
func (i *Images) List(ctx context.Context) ([]images.Image, error) {
	var apiImages []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Size     int64    `json:"Size"`
		Created  int64    `json:"Created"`
	}
	if err := i.getJSON(ctx, "/images/json", &apiImages); err != nil {
		return nil, err
	}
	var apiContainers []struct {
		ImageID string `json:"ImageID"`
	}
	if err := i.getJSON(ctx, "/containers/json?"+url.Values{"all": {"1"}}.Encode(), &apiContainers); err != nil {
		return nil, err
	}
	used := make(map[string]bool, len(apiContainers))
	for _, container := range apiContainers {
		used[container.ImageID] = true
	}

	list := make([]images.Image, 0, len(apiImages))
	for _, api := range apiImages {
		tags := make([]string, 0, len(api.RepoTags))
		for _, tag := range api.RepoTags {
			if tag != "<none>:<none>" {
				tags = append(tags, tag)
			}
		}
		list = append(list, images.Image{
			ID:      api.ID,
			Tags:    tags,
			Size:    api.Size,
			Created: time.Unix(api.Created, 0),
			InUse:   used[api.ID],
		})
	}
	return list, nil
}

// Load streams archive (a `docker save` tar of size bytes) into POST
// /images/load?quiet=1 and returns the names the daemon reports loading. The
// daemon answers 200 and reports a failure inside its JSON-lines stream, so
// the stream is decoded to the end: an error line fails the load whatever
// names preceded it, and a stream naming nothing is a failure too.
func (i *Images) Load(ctx context.Context, archive io.Reader, size int64) ([]string, error) {
	path := "/images/load?" + url.Values{"quiet": {"1"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+path, &ctxReader{ctx: ctx, r: archive})
	if err != nil {
		return nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := i.do(ctx, path, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if !ok(resp.StatusCode) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		var daemon struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &daemon) == nil && daemon.Message != "" {
			msg = daemon.Message
		}
		return nil, fmt.Errorf("%w: %s", images.ErrLoadFailed, msg)
	}

	var names []string
	decoder := json.NewDecoder(resp.Body)
	for {
		var line struct {
			Stream      string `json:"stream"`
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		err := decoder.Decode(&line)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: read load stream: %v", images.ErrLoadFailed, err)
		}
		if msg := firstNonEmpty(line.ErrorDetail.Message, line.Error); msg != "" {
			return nil, fmt.Errorf("%w: %s", images.ErrLoadFailed, msg)
		}
		for _, prefix := range []string{"Loaded image: ", "Loaded image ID: "} {
			if name, found := strings.CutPrefix(line.Stream, prefix); found {
				names = append(names, strings.TrimSpace(name))
				break
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: archive contained no images", images.ErrLoadFailed)
	}
	return names, nil
}

func (i *Images) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return fmt.Errorf("docker api %s: %w", path, err)
	}
	resp, err := i.do(ctx, path, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !ok(resp.StatusCode) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return apiError(path, resp.Status, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	return nil
}

// do runs the request, mapping a failure to dial the socket to
// images.ErrUnavailable — unless the context ended, which is reported as is.
func (i *Images) do(ctx context.Context, path string, req *http.Request) (*http.Response, error) {
	resp, err := i.client.Do(req)
	if err != nil {
		var opErr *net.OpError
		if ctx.Err() == nil && errors.As(err, &opErr) && opErr.Op == "dial" {
			return nil, fmt.Errorf("%w: %v", images.ErrUnavailable, err)
		}
		return nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	return resp, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ctxReader makes a blocked Read give up when ctx ends. The HTTP transport
// cannot interrupt a request-body Read already in flight, so without this a
// stalled upload would outlive its cancelled request. The abandoned Read
// finishes on its own goroutine once the underlying reader returns (the
// handler's request body is closed when the client goes away).
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	type result struct {
		n   int
		err error
	}
	buf := make([]byte, len(p))
	done := make(chan result, 1)
	go func() {
		n, err := c.r.Read(buf)
		done <- result{n, err}
	}()
	select {
	case res := <-done:
		copy(p, buf[:res.n])
		return res.n, res.err
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	}
}
