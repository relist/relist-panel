package composes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExecutionSnapshot is private execution input, persisted independently of
// Deployment history. It contains values and must never be used as API output.
type ExecutionSnapshot struct {
	YAML          string
	Environment   string
	Sets          map[int64]string
	Warnings      []string
	Services      []string
	Secrets       []string
	PathVariables map[string]string
	// ZeroDowntime is the Services this deploy rolls out (ADR-0023), in YAML
	// order; empty in snapshots taken before the feature.
	ZeroDowntime []string
}

func (x *ExecutionSnapshot) files() deployFiles {
	return deployFiles{composeYAML: x.YAML, envFile: x.Environment, setFiles: x.Sets, warnings: x.Warnings, zeroDowntime: x.ZeroDowntime}
}

// Capture resolves every mutable configuration input once. The caller holds
// the Project gate, including while the resulting snapshot is persisted.
func (s *Service) Capture(ctx context.Context, t DeployTarget) (DeployTarget, error) {
	if err := s.CheckDeployable(ctx, t); err != nil {
		return DeployTarget{}, err
	}
	files, err := s.prepareDeployFiles(ctx, t)
	if err != nil {
		return DeployTarget{}, err
	}
	services, err := buildServices(t.Compose.YAMLContent)
	if err != nil {
		return DeployTarget{}, err
	}
	t.Snapshot = &ExecutionSnapshot{YAML: files.composeYAML, Environment: files.envFile, Sets: files.setFiles, Warnings: files.warnings, Services: services, Secrets: files.secrets, PathVariables: files.pathVariables, ZeroDowntime: files.zeroDowntime}
	return t, nil
}

func prepareSnapshotBuild(t DeployTarget) (BuildPlan, error) {
	dir := ComposeDir(t.Request)
	// Build staging has its own set files and never overwrites files used by
	// the running Compose. Their values still come from the same snapshot.
	req, cleanup, err := stageSnapshot(t, filepath.Join(dir, buildComposeFileName), filepath.Join(dir, buildEnvFileName), filepath.Join(dir, ".build-sets"))
	if err != nil {
		return BuildPlan{}, err
	}
	return BuildPlan{Request: req, Services: t.Snapshot.Services, Cleanup: cleanup}, nil
}

// stageSnapshot writes the snapshot's YAML, env file and set files at the
// given paths, the YAML pointing at the staged env and set files, and returns
// the request that reads them. cleanup removes all of them.
func stageSnapshot(t DeployTarget, composePath, envPath, setsPath string) (RunRequest, func(), error) {
	req := RunRequest{ProjectName: t.Request.ProjectName, ComposeFilePath: composePath, EnvFilePath: envPath}
	cleanup := func() {
		_ = os.Remove(composePath)
		_ = os.Remove(envPath)
		_ = os.RemoveAll(setsPath)
	}
	staged, err := rewriteYAMLPaths(t.Snapshot.YAML, func(value string) string {
		if value == t.Request.EnvFilePath {
			return envPath
		}
		if strings.HasPrefix(value, setsDir(t.Request.EnvFilePath)+string(os.PathSeparator)) {
			return filepath.Join(setsPath, filepath.Base(value))
		}
		return value
	})
	if err != nil {
		return RunRequest{}, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(composePath), composeDirPerm); err != nil {
		return RunRequest{}, nil, err
	}
	if len(t.Snapshot.Sets) > 0 {
		if err := os.MkdirAll(setsPath, composeDirPerm); err != nil {
			cleanup()
			return RunRequest{}, nil, err
		}
		for id, content := range t.Snapshot.Sets {
			if err := os.WriteFile(filepath.Join(setsPath, fmt.Sprintf("%d.env", id)), []byte(content), envFilePerm); err != nil {
				cleanup()
				return RunRequest{}, nil, err
			}
		}
	}
	for path, content := range map[string]string{envPath: t.Snapshot.Environment, composePath: staged} {
		if err := os.WriteFile(path, []byte(content), envFilePerm); err != nil {
			cleanup()
			return RunRequest{}, nil, err
		}
	}
	return req, cleanup, nil
}

// WithSourceDirectory points relative Source paths at one Deployment's private
// tree, keeping the Docker project identity and other host paths unchanged.
func (t DeployTarget) WithSourceDirectory(dir string) (DeployTarget, error) {
	if t.Snapshot == nil {
		return t, nil
	}
	copy := *t.Snapshot
	content, err := rewriteYAMLPaths(copy.YAML, func(value string) string {
		expanded, ok := expandPath(value, copy.PathVariables)
		if !ok {
			return value
		}
		path := filepath.Clean(expanded)
		if filepath.IsAbs(path) {
			if rel, err := filepath.Rel(ComposeDir(t.Request), path); err == nil {
				path = rel
			}
		}
		if path == "src" || strings.HasPrefix(path, "src/") {
			return strings.ReplaceAll(filepath.Join(dir, strings.TrimPrefix(path, "src")), "$", "$$")
		}
		return value
	}, pathRewriteOptions{Variables: copy.PathVariables, BuildContext: func(value string) string {
		if relative, ok := t.buildContextRelative(value); ok {
			return strings.ReplaceAll(filepath.Join(filepath.Dir(dir), relative), "$", "$$")
		}
		return value
	}})
	if err != nil {
		return DeployTarget{}, err
	}
	copy.YAML = content
	t.Snapshot = &copy
	return t, nil
}

// Only file-bearing Compose fields are rewritten; environment, labels and
// command strings can contain the same text without being paths.
type pathRewriteOptions struct {
	Variables    map[string]string
	BuildContext func(string) string
}

func rewriteYAMLPaths(content string, rewrite func(string) string, options ...pathRewriteOptions) (string, error) {
	var opts pathRewriteOptions
	if len(options) > 0 {
		opts = options[0]
	}
	contextRewrite := rewrite
	if opts.BuildContext != nil {
		contextRewrite = opts.BuildContext
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", err
	}
	var rewriteValue func(any) any
	rewriteValue = func(v any) any {
		switch v := v.(type) {
		case string:
			return rewrite(v)
		case []any:
			for i, value := range v {
				v[i] = rewriteValue(value)
			}
			return v
		case map[string]any:
			for _, field := range []string{"path", "env_file", "project_directory"} {
				if value, ok := v[field]; ok {
					v[field] = rewriteValue(value)
				}
			}
			return v
		}
		return v
	}
	for _, section := range []string{"configs", "secrets"} {
		if entries, ok := doc[section].(map[string]any); ok {
			for _, body := range entries {
				if body, ok := body.(map[string]any); ok {
					if file, ok := body["file"].(string); ok {
						body["file"] = rewrite(file)
					}
				}
			}
		}
	}
	if includes, ok := doc["include"].([]any); ok {
		for i, value := range includes {
			includes[i] = rewriteValue(value)
		}
	}
	if services, ok := doc["services"].(map[string]any); ok {
		for _, body := range services {
			svc, ok := body.(map[string]any)
			if !ok {
				continue
			}
			switch build := svc["build"].(type) {
			case string:
				svc["build"] = contextRewrite(build)
			case map[string]any:
				if _, ok := build["context"]; !ok {
					build["context"] = "."
				}
				if p, ok := build["context"].(string); ok {
					build["context"] = contextRewrite(p)
				}
				if p, ok := build["dockerfile"].(string); ok {
					expanded, valid := expandPath(p, opts.Variables)
					if valid && filepath.IsAbs(expanded) {
						build["dockerfile"] = contextRewrite(p)
					}
				}
				switch contexts := build["additional_contexts"].(type) {
				case map[string]any:
					for key, value := range contexts {
						if path, ok := value.(string); ok {
							contexts[key] = contextRewrite(path)
						}
					}
				case []any:
					for i, value := range contexts {
						if value, ok := value.(string); ok {
							name, path, found := strings.Cut(value, "=")
							if found {
								contexts[i] = name + "=" + contextRewrite(path)
							}
						}
					}
				}
			}
			for _, field := range []string{"env_file", "label_file"} {
				if value, ok := svc[field]; ok {
					svc[field] = rewriteValue(value)
				}
			}
			if mounts, ok := svc["volumes"].([]any); ok {
				for i, mount := range mounts {
					switch mount := mount.(type) {
					case string:
						path, rest, ok := shortMountParts(mount)
						expanded, _ := expandPath(path, opts.Variables)
						if ok && (strings.HasPrefix(expanded, ".") || filepath.IsAbs(expanded)) {
							mounts[i] = rewrite(path) + ":" + rest
						}
					case map[string]any:
						if mount["type"] == "bind" {
							if p, ok := mount["source"].(string); ok {
								mount["source"] = rewrite(p)
							}
						}
					}
				}
			}
			if extends, ok := svc["extends"].(map[string]any); ok {
				if p, ok := extends["file"].(string); ok {
					extends["file"] = rewrite(p)
				}
			}
			if develop, ok := svc["develop"].(map[string]any); ok {
				if watch, ok := develop["watch"].([]any); ok {
					for _, item := range watch {
						if item, ok := item.(map[string]any); ok {
							if path, ok := item["path"].(string); ok {
								item["path"] = rewrite(path)
							}
						}
					}
				}
			}
		}
	}
	out, err := yaml.Marshal(doc)
	return string(out), err
}

func (t DeployTarget) buildContextRelative(value string) (string, bool) {
	if t.Snapshot == nil {
		return "", false
	}
	expanded, ok := expandPath(value, t.Snapshot.PathVariables)
	if !ok || strings.Contains(expanded, "://") || strings.HasPrefix(expanded, "git@") || strings.HasPrefix(expanded, "service:") || strings.HasPrefix(expanded, "docker-image:") {
		return "", false
	}
	if filepath.IsAbs(expanded) {
		expanded, _ = filepath.Rel(ComposeDir(t.Request), expanded)
	}
	expanded = filepath.Clean(expanded)
	if expanded == ".." || strings.HasPrefix(expanded, "../") {
		return "", false
	}
	return expanded, true
}

// NeedsComposeBuildContext reports Source-backed builds whose context also
// reads files next to src/. Capture those files before the canonical tree edits.
func (t DeployTarget) NeedsComposeBuildContext() bool {
	if t.Snapshot == nil {
		return false
	}
	var doc struct {
		Services map[string]struct{ Build any }
	}
	if yaml.Unmarshal([]byte(t.Snapshot.YAML), &doc) != nil {
		return false
	}
	outsideSource := func(value string) bool {
		relative, ok := t.buildContextRelative(value)
		return ok && relative != "src" && !strings.HasPrefix(relative, "src/")
	}
	for _, svc := range doc.Services {
		switch build := svc.Build.(type) {
		case string:
			if outsideSource(build) {
				return true
			}
		case map[string]any:
			if _, ok := build["context"]; !ok {
				return true
			}
			if context, ok := build["context"].(string); ok && outsideSource(context) {
				return true
			}
			if dockerfile, ok := build["dockerfile"].(string); ok {
				expanded, valid := expandPath(dockerfile, t.Snapshot.PathVariables)
				if valid && filepath.IsAbs(expanded) && outsideSource(dockerfile) {
					return true
				}
				if valid && !filepath.IsAbs(expanded) {
					if context, ok := build["context"].(string); ok {
						if relative, inside := t.buildContextRelative(context); inside && outsideSource(filepath.Join(relative, expanded)) {
							return true
						}
					}
				}
			}
			switch contexts := build["additional_contexts"].(type) {
			case map[string]any:
				for _, v := range contexts {
					if path, ok := v.(string); ok && outsideSource(path) {
						return true
					}
				}
			case []any:
				for _, v := range contexts {
					if v, ok := v.(string); ok {
						_, path, found := strings.Cut(v, "=")
						if found && outsideSource(path) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func shortMountParts(mount string) (string, string, bool) {
	depth := 0
	for i, char := range mount {
		switch char {
		case '{':
			depth++
		case '}':
			depth--
		case ':':
			if depth == 0 {
				return mount[:i], mount[i+1:], true
			}
		}
	}
	return mount, "", false
}

// CleanStaging removes the snapshot copies a zero-downtime check or a build
// stages in dir. They hold env values, and a panel that stops mid-run leaves
// them behind; at startup nothing is using them. A missing dir is fine.
func CleanStaging(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".zd-check-") || name == buildComposeFileName || name == buildEnvFileName || name == ".build-sets" {
			if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}
