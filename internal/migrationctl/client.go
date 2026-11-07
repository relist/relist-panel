// Package migrationctl is the client side of the panel's migration control
// socket (ADR-0019): what `relist-panel migration` uses to drive the
// Migration the running panel performs.
package migrationctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"relist-panel/internal/migration"
)

// ErrNoPanel means the panel did not answer on the socket: it is not running
// in this container, or it is re-executing at finalize.
var ErrNoPanel = errors.New("the panel is not answering on its control socket")

// APIError is a refusal from the panel, with its message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Token is a Migration token, shown once.
type Token struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// requestTimeout bounds every call except Start and Abort, which wait for
// the old host.
const requestTimeout = 30 * time.Second

type Client struct {
	path string
	http *http.Client
}

// New returns a client for the control socket at socketPath.
func New(socketPath string) *Client {
	return &Client{path: socketPath, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		// A re-executed panel is a new process: never reuse a connection
		// across polls.
		DisableKeepAlives: true,
	}}}
}

// Raw sends one request and returns the status and body as they are, for
// `relist-panel migration api`.
func (c *Client) Raw(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://panel"+path, r)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, noPanel(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, noPanel(err)
	}
	return resp.StatusCode, out, nil
}

// noPanel wraps the errors that mean nobody answered as ErrNoPanel.
func noPanel(err error) error {
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: %v", ErrNoPanel, err)
	}
	return err
}

// call sends in as JSON (nil for none) and decodes a 2xx answer into out (nil
// to ignore it). timeout 0 means none.
func (c *Client) call(ctx context.Context, timeout time.Duration, method, path string, in, out any) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	status, raw, err := c.Raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("the panel answered %d", status)
		}
		return &APIError{Status: status, Message: e.Error}
	}
	if out == nil || status == http.StatusNoContent || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) Progress(ctx context.Context) (migration.Progress, error) {
	var p migration.Progress
	err := c.call(ctx, requestTimeout, http.MethodGet, "/api/migration/pull/progress", nil, &p)
	return p, err
}

func (c *Client) Connect(ctx context.Context, oldHostURL, token, selfURL string) (migration.Progress, error) {
	var p migration.Progress
	err := c.call(ctx, requestTimeout, http.MethodPost, "/api/migration/pull/connect",
		map[string]string{"sourceUrl": oldHostURL, "token": token, "selfUrl": selfURL}, &p)
	return p, err
}

func (c *Client) Recheck(ctx context.Context, hostPaths []string) (migration.Progress, error) {
	var p migration.Progress
	err := c.call(ctx, requestTimeout, http.MethodPost, "/api/migration/pull/recheck", map[string]any{"hostPaths": nonNil(hostPaths)}, &p)
	return p, err
}

// Start locks the old server and pulls; includeRegistry also moves the
// Registry's data (off by default, ADR-0033).
func (c *Client) Start(ctx context.Context, hostPaths []string, includeRegistry bool) error {
	return c.call(ctx, 0, http.MethodPost, "/api/migration/pull/start",
		map[string]any{"hostPaths": nonNil(hostPaths), "includeRegistry": includeRegistry}, nil)
}

func (c *Client) Retry(ctx context.Context) error {
	return c.call(ctx, requestTimeout, http.MethodPost, "/api/migration/pull/retry", nil, nil)
}

func (c *Client) Abort(ctx context.Context) error {
	return c.call(ctx, 0, http.MethodPost, "/api/migration/pull/abort", nil, nil)
}

func (c *Client) CreateToken(ctx context.Context, lifetime string) (Token, error) {
	var t Token
	err := c.call(ctx, requestTimeout, http.MethodPost, "/api/migration/tokens", map[string]string{"lifetime": lifetime}, &t)
	return t, err
}

func (c *Client) RevokeToken(ctx context.Context) error {
	return c.call(ctx, requestTimeout, http.MethodDelete, "/api/migration/tokens/current", nil, nil)
}

func (c *Client) Status(ctx context.Context) (migration.OldHostStatus, error) {
	var s migration.OldHostStatus
	err := c.call(ctx, requestTimeout, http.MethodGet, "/api/migration/status", nil, &s)
	return s, err
}

func (c *Client) Resume(ctx context.Context) (string, error) {
	var out struct {
		Warning string `json:"warning"`
	}
	err := c.call(ctx, 0, http.MethodPost, "/api/migration/resume", map[string]string{"confirm": "RESUME"}, &out)
	return out.Warning, err
}

func (c *Client) Activity(ctx context.Context) (migration.Activity, error) {
	var a migration.Activity
	err := c.call(ctx, requestTimeout, http.MethodGet, "/api/migration/serve/activity", nil, &a)
	return a, err
}

// nonNil keeps an empty selection a JSON array, not null.
func nonNil(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}
