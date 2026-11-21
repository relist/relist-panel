package databases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// waitHealthy waits for the container's healthcheck to pass. A container that
// is not running fails at once, with the end of its log.
func (s *Service) waitHealthy(ctx context.Context, name string) error {
	deadline := time.Now().Add(s.healthTimeout)
	for {
		st, err := s.d.Host.Inspect(ctx, name)
		if err != nil {
			return err
		}
		if !st.Exists || !st.Running {
			logs, _ := s.d.Host.Logs(ctx, name, 20)
			msg := "the container stopped"
			if logs = strings.TrimSpace(redactPasswords(logs)); logs != "" {
				msg += ":\n" + logs
			}
			return errors.New(msg)
		}
		if st.Health == "healthy" {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the container did not become healthy within %s", s.healthTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.healthPoll):
		}
	}
}
