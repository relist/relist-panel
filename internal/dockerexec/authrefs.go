package dockerexec

import (
	"context"
	"strings"
)

// RunningAuthRefs lists the running Containers with a Traefik router whose
// middlewares name middleware (e.g. "relist-auth-3@file"). Labels are what
// Traefik routes by, so a Domain changed in the panel but not redeployed
// still counts (ADR-0036).
func RunningAuthRefs(ctx context.Context, middleware string) ([]string, error) {
	ids, err := dockerOutput(ctx, "ps", "-q")
	if err != nil {
		return nil, err
	}
	containers := strings.Fields(ids)
	if len(containers) == 0 {
		return nil, nil
	}
	format := `{{.Name}}{{range $k, $v := .Config.Labels}}{{"\t"}}{{$k}}={{$v}}{{end}}`
	out, err := dockerOutput(ctx, append([]string{"inspect", "--format", format}, containers...)...)
	if err != nil {
		return nil, err
	}
	return parseAuthRefs(out, middleware), nil
}

// parseAuthRefs reads RunningAuthRefs' inspect output: one line per
// Container, its name then tab-separated key=value labels.
func parseAuthRefs(out, middleware string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		name := strings.TrimPrefix(strings.TrimSpace(fields[0]), "/")
		if name == "" {
			continue
		}
	labels:
		for _, label := range fields[1:] {
			key, value, ok := strings.Cut(label, "=")
			if !ok || !strings.HasPrefix(key, "traefik.http.routers.") || !strings.HasSuffix(key, ".middlewares") {
				continue
			}
			for _, m := range strings.Split(value, ",") {
				if strings.TrimSpace(m) == middleware {
					names = append(names, name)
					break labels
				}
			}
		}
	}
	return names
}
