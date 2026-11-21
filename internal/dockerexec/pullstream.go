package dockerexec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// stream POSTs path and reads its progress stream to the end, failing on an
// error line (a pull reports errors in-band with a 200).
func (e *Engine) stream(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker api POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("pull %s: %s", path, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var line struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line.Error != "" {
			return errors.New(line.Error)
		}
	}
	return scanner.Err()
}
