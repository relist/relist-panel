package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// client calls the old host's Migration routes.
type client struct {
	http       *http.Client
	base       string
	sessionKey string
}

// remoteError is a non-2xx answer from the old host.
type remoteError struct {
	Status  int
	Message string
}

func (e *remoteError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the source answered %d", e.Status)
	}
	return e.Message
}

func (c *client) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.sessionKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.sessionKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("reach the source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return resp.StatusCode, &remoteError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode the source's answer to %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

func (c *client) claim(ctx context.Context, token, claimedBy string) (string, error) {
	var out struct {
		SessionKey string `json:"sessionKey"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/api/migration/claim", map[string]string{"token": token, "claimedBy": claimedBy}, &out); err != nil {
		return "", err
	}
	return out.SessionKey, nil
}

func (c *client) preflight(ctx context.Context) (SourceInfo, error) {
	var info SourceInfo
	_, err := c.do(ctx, http.MethodGet, "/api/migration/preflight", nil, &info)
	return info, err
}

// start asks the source to lock. It returns the manifest once ready, or the
// lock step and any error of the last attempt while it is still running.
func (c *client) start(ctx context.Context, hostPaths []string) (m *Manifest, step Step, lastErr string, err error) {
	var raw json.RawMessage
	status, err := c.do(ctx, http.MethodPost, "/api/migration/start", map[string]any{"hostPaths": hostPaths}, &raw)
	if err != nil {
		return nil, "", "", err
	}
	if status == http.StatusOK {
		var manifest Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, "", "", err
		}
		return &manifest, StepReady, "", nil
	}
	var pending struct {
		Step  Step   `json:"step"`
		Error string `json:"error"`
	}
	json.Unmarshal(raw, &pending)
	return nil, pending.Step, pending.Error, nil
}

// item opens one item's stream.
func (c *client) item(ctx context.Context, id string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/migration/items/"+id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.sessionKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach the source: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &remoteError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	return resp.Body, nil
}

func (c *client) digest(ctx context.Context, id string) (string, error) {
	var out struct {
		Sha256 string `json:"sha256"`
	}
	_, err := c.do(ctx, http.MethodGet, "/api/migration/items/"+id+"/digest", nil, &out)
	return out.Sha256, err
}

func (c *client) abort(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/api/migration/abort", nil, nil)
	return err
}

func (c *client) finish(ctx context.Context, targetURL string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/migration/finish", map[string]string{"targetUrl": targetURL}, nil)
	return err
}
