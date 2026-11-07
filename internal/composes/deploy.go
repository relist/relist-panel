package composes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/certificates"
	"relist-panel/internal/dockernet"
	"relist-panel/internal/domains"
	"relist-panel/internal/policy"
)

const (
	buildComposeFileName = ".build-compose.yml"
	buildEnvFileName     = ".build.env"
)

// ErrBlankYAML is returned by CheckDeployable for a Compose with nothing to deploy.
var ErrBlankYAML = errors.New("compose has no YAML content to deploy")

// DeployTarget is one Compose loaded for a deploy: the Project it belongs to,
// the row, and the files a runner acts on.
type DeployTarget struct {
	ProjectID int64
	Compose   Compose
	Request   RunRequest
	Snapshot  *ExecutionSnapshot `json:"-"`
	// SourceDir is this Deployment's private Source copy, which its paths
	// point at (WithSourceDirectory); empty without one.
	SourceDir string
}

// DeployOptions tunes RunDeploy. FreshVolumes deletes the Compose's named
// volumes first, Build passes --build to the runner and Out, when set,
// receives the deploy's warnings and the runner's output. Exclude leaves
// those Services to their Rollout (ADR-0023): up then starts only the rest,
// and the deploy time waits for MarkDeployed.
type DeployOptions struct {
	FreshVolumes, Build bool
	Exclude             []string
	Out                 io.Writer
}

// FailureKind says who is to blame for a DeployFailure, so a caller can map
// it to its own status (HTTP 400, 502 or 500 for the handlers).
type FailureKind int

const (
	// FailureInternal is a panel-side failure: database or disk.
	FailureInternal FailureKind = iota
	// FailureInvalid is a Compose the panel cannot resolve: bad YAML or a bad domain target.
	FailureInvalid
	// FailureRunner is Docker failing to do what the panel asked.
	FailureRunner
)

// DeployFailure is the error RunDeploy and the preview return. Error() is the
// user-facing message.
type DeployFailure struct {
	Kind           FailureKind
	Message        string
	Err            error
	VolumesRemoved bool
}

func (e *DeployFailure) Error() string { return e.Message }
func (e *DeployFailure) Unwrap() error { return e.Err }

func failure(kind FailureKind, message string, err error) *DeployFailure {
	return &DeployFailure{Kind: kind, Message: message, Err: err}
}

func writeDeployFailure(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var f *DeployFailure
	if errors.As(err, &f) {
		switch f.Kind {
		case FailureInvalid:
			status = http.StatusBadRequest
		case FailureRunner:
			status = http.StatusBadGateway
		}
	}
	writeError(w, status, err.Error())
}

// DeploymentCanceler stops Deployments of Composes that are about to be torn down.
type DeploymentCanceler interface {
	CancelForComposes(ctx context.Context, composeIDs []int64)
}

// SetDeploymentCanceler wires the Deployment queue in. It is optional: nil
// means teardown and delete cancel nothing.
func (s *Service) SetDeploymentCanceler(c DeploymentCanceler) { s.canceler = c }

func (s *Service) cancelDeployments(ctx context.Context, composeIDs ...int64) {
	if s.canceler != nil && len(composeIDs) > 0 {
		s.canceler.CancelForComposes(ctx, composeIDs)
	}
}

// ComposeDir is the directory holding everything the panel keeps for a Compose.
func ComposeDir(req RunRequest) string { return filepath.Dir(req.ComposeFilePath) }

// Target loads a Compose, its Project and its RunRequest. It returns an error
// matching sql.ErrNoRows when the Compose is gone.
func (s *Service) Target(ctx context.Context, composeID int64) (DeployTarget, error) {
	var projectID int64
	row := s.repository.db.QueryRowContext(ctx, `
		SELECT c.id, c.environment_id, c.name, c.project_slug, c.env_slug, c.yaml_content, c.created_at, c.updated_at, c.deployed_at, c.identity_version, c.domains_changed_at, c.variables_changed_at, e.project_id
		FROM composes c JOIN environments e ON e.id = c.environment_id
		WHERE c.id = ?
	`, composeID)
	compose, err := scanComposeValues(func(dest ...any) error { return row.Scan(append(dest, &projectID)...) })
	if err != nil {
		return DeployTarget{}, fmt.Errorf("load compose %d: %w", composeID, err)
	}
	return DeployTarget{ProjectID: projectID, Compose: compose, Request: runRequestFor(s.dataDir, compose)}, nil
}

// CheckDeployable runs the checks every deploy shares, touching nothing: the
// YAML must not be blank and the deploy identity must be unambiguous.
func (s *Service) CheckDeployable(ctx context.Context, t DeployTarget) error {
	if strings.TrimSpace(t.Compose.YAMLContent) == "" {
		return ErrBlankYAML
	}
	return s.requireUniqueIdentity(ctx, t.Compose)
}

// deployFiles is the content a deploy writes, computed before anything is
// removed or written.
type deployFiles struct {
	composeYAML string
	envFile     string
	// setFiles is the content of each sets/<id>.env the resolved YAML points at.
	setFiles      map[int64]string
	warnings      []string
	secrets       []string
	pathVariables map[string]string
	// zeroDowntime is the switched-on Services that have a Domain, in YAML
	// order: the ones whose labels carry the retry and the transport.
	zeroDowntime []string
}

// prepareDeployFiles resolves the YAML and loads the environment variables.
// It touches no files.
func (s *Service) prepareDeployFiles(ctx context.Context, t DeployTarget) (deployFiles, error) {
	if t.Snapshot != nil {
		return t.Snapshot.files(), nil
	}
	vars, err := s.repository.loadDeployVariables(ctx, t.Compose, true)
	if err != nil {
		return deployFiles{}, failure(FailureInternal, "could not load variables", err)
	}
	// Read once: the labels and the snapshot's list must agree, or a Service
	// could roll out without its retry.
	enabled, err := s.switches.Enabled(ctx, t.Compose.ID)
	if err != nil {
		return deployFiles{}, failure(FailureInternal, "could not load zero-downtime services", err)
	}
	resolvedYAML, usedSets, warnings, zeroDowntime, err := s.resolveForDeploy(ctx, t.ProjectID, t.Compose, t.Compose.YAMLContent, t.Request.EnvFilePath, vars, enabled)
	if err != nil {
		return deployFiles{}, err
	}
	return deployFiles{
		composeYAML:   resolvedYAML,
		envFile:       renderEnvFile(mergeVariables(vars.Environment, vars.Compose, nil).Interpolated),
		setFiles:      vars.setFiles(usedSets),
		warnings:      warnings,
		secrets:       vars.secretValues(),
		pathVariables: vars.interpolationValues(),
		zeroDowntime:  zeroDowntime,
	}, nil
}

// resolveForDeploy loads the Compose's domains and resolves yamlContent the
// way a deploy writes it, returning the IDs of the sets it points at. vars
// only has to carry keys, so preview can share it without reading values. It
// returns a *DeployFailure. The enabled Services that have a Domain get the
// zero-downtime labels and are returned in YAML order.
func (s *Service) resolveForDeploy(ctx context.Context, projectID int64, compose Compose, yamlContent, envFilePath string, vars deployVariables, enabled []string) (string, []int64, []string, []string, error) {
	composeDomains, err := s.domains.ListForCompose(ctx, compose.ID)
	if err != nil {
		return "", nil, nil, nil, failure(FailureInternal, "could not load domains", err)
	}
	projectName, environmentName, err := s.repository.parentNames(ctx, projectID, compose.EnvironmentID)
	if err != nil {
		return "", nil, nil, nil, failure(FailureInternal, "could not load compose", err)
	}
	zeroDowntime, err := zeroDowntimeServices(yamlContent, composeDomains, enabled)
	if err != nil {
		return "", nil, nil, nil, failure(FailureInvalid, "compose yaml is invalid: "+err.Error(), err)
	}
	onSet := map[string]bool{}
	for _, name := range zeroDowntime {
		onSet[name] = true
	}
	id := networkIdentity{DeployName: compose.DeployProjectName(), Compose: compose.Name, Environment: environmentName, Project: projectName}
	resolvedYAML, usedSets, warnings, err := resolveCompose(yamlContent, dockernet.NetworkName, envFilePath, compose.ID, composeDomains, id, vars, onSet)
	var target *domains.TargetError
	if errors.As(err, &target) {
		return "", nil, nil, nil, failure(FailureInvalid, target.Error(), err)
	} else if err != nil {
		return "", nil, nil, nil, failure(FailureInvalid, "compose yaml is invalid: "+err.Error(), err)
	}
	hasSource, err := s.repository.hasSource(ctx, compose.ID)
	if err != nil {
		return "", nil, nil, nil, failure(FailureInternal, "could not load source", err)
	}
	elsewhere, err := s.volumeNamesElsewhere(ctx, []int64{compose.ID})
	if err != nil {
		return "", nil, nil, nil, failure(FailureInternal, "could not load compose volumes", err)
	}
	warnings = append(warnings, volumeWarnings(yamlContent, filepath.Dir(envFilePath), hasSource, elsewhere)...)
	if warnings == nil {
		warnings = []string{}
	}
	return resolvedYAML, usedSets, warnings, zeroDowntime, nil
}

// RunDeploy resolves and writes the Compose's files, runs `up` and records the
// deploy time. With FreshVolumes the volumes go first, after everything that
// can be checked without touching Docker or the disk, and the down step runs
// against the files already on disk before the new ones replace them. It
// returns a *DeployFailure for every failure it names.
func (s *Service) RunDeploy(ctx context.Context, t DeployTarget, opts DeployOptions) (warnings []string, deployErr error) {
	req := t.Request
	files, err := s.prepareDeployFiles(ctx, t)
	if err != nil {
		return nil, err
	}
	// The zero-downtime labels name this transport; without it Traefik
	// disables their Domains.
	if len(files.zeroDowntime) > 0 {
		if err := domains.WriteTransportFile(certificates.DynamicDir(s.dataDir)); err != nil {
			return nil, failure(FailureInternal, "could not write Traefik transport file", err)
		}
	}

	// After the volumes are gone a failure must say so.
	suffix := ""
	removed := false
	defer func() {
		var f *DeployFailure
		if removed && errors.As(deployErr, &f) {
			f.VolumesRemoved = true
		}
	}()
	if opts.FreshVolumes {
		if _, err := os.Stat(req.ComposeFilePath); err == nil {
			plan, removedAny, err := s.destroyStack(ctx, t.Compose, req, []int64{t.Compose.ID})
			removed = removedAny
			if opts.Out != nil {
				for _, kept := range plan.Keep {
					fmt.Fprintf(opts.Out, "kept volume %s: %s\n", kept.Name, kept.Reason)
				}
			}
			if err != nil {
				return nil, failure(FailureRunner, "delete volumes failed: "+err.Error(), err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, failure(FailureInternal, "could not inspect compose files", err)
		}
		if removed {
			suffix = " after volumes were removed"
		}
	}

	if message, err := writeFiles(req, files); err != nil {
		if removed {
			return nil, failure(FailureRunner, message+suffix+": "+err.Error()+". "+volumesRemovedAdvice, err)
		}
		return nil, failure(FailureInternal, message, err)
	}
	if opts.Out != nil {
		for _, w := range files.warnings {
			fmt.Fprintf(opts.Out, "warning: %s\n", w)
		}
	}
	imageRefs, err := s.prepareImages(ctx, req, opts.Build, opts.Out)
	if err != nil {
		var f *DeployFailure
		if errors.As(err, &f) {
			f.Message += suffix + volumesAdvice(removed)
		}
		return nil, err
	}
	// prepareImages built and pulled everything: up starts what it checked.
	opts.Build = false
	if len(opts.Exclude) > 0 {
		if err := s.deploySubset(ctx, req, files.composeYAML, opts); err != nil {
			return nil, failure(FailureRunner, "deploy failed: "+err.Error(), err)
		}
		if err := s.repository.setImageRefs(ctx, t.Compose.ID, imageRefs); err != nil {
			return nil, failure(FailureInternal, "could not record the deployed images", err)
		}
		return files.warnings, nil
	}
	if err := s.runner.Up(ctx, req, UpOptions{NoPull: true, Out: opts.Out}); err != nil {
		return nil, failure(FailureRunner, "deploy failed"+suffix+": "+err.Error()+volumesAdvice(removed), err)
	}

	// The deploy timestamp records success only (deploy-timestamps spec): it
	// lands after the runner returns without error, and no other action ever
	// writes it — "has deployed" and "is running" stay two different facts.
	deployedAt := s.now().UTC().Truncate(time.Second)
	if err := s.repository.markDeployed(ctx, t.Compose.EnvironmentID, t.Compose.ID, deployedAt); err != nil {
		return nil, failure(FailureInternal, "could not record deploy time", err)
	}
	if err := s.repository.setImageRefs(ctx, t.Compose.ID, imageRefs); err != nil {
		return nil, failure(FailureInternal, "could not record the deployed images", err)
	}
	return files.warnings, nil
}

// authRefused matches a registry refusing a pull's login, not a local
// "permission denied".
var authRefused = regexp.MustCompile(`unauthorized|authentication required|pull access denied|\bdenied: `)

// pullHint explains a pull a registry refused (ADR-0034): with a stored
// External registry login for one of the images' hosts, that login was
// refused; without one, the image may need one. Other failures get none.
func (s *Service) pullHint(ctx context.Context, err error, images []string) string {
	if hint := s.hairpinHint(ctx, err); hint != "" {
		return hint
	}
	if !authRefused.MatchString(strings.ToLower(err.Error())) {
		return ""
	}
	var stored []string
	if s.registryLogins != nil {
		if hosts, lerr := s.registryLogins(ctx); lerr == nil {
			for _, image := range images {
				host := imageHost(image)
				if slices.Contains(hosts, host) && !slices.Contains(stored, host) {
					stored = append(stored, host)
				}
			}
		}
	}
	if len(stored) > 0 {
		return "Registry refused the stored login for " + strings.Join(stored, ", ") + ". Check it with Test on Registry → External registries."
	}
	return "If the image is private, add its registry's credentials on Registry → External registries."
}

// unreachable matches a pull that never reached a registry.
var unreachable = regexp.MustCompile(`dial tcp|no such host|connection refused|i/o timeout|x509|tls:`)

// hairpinHint explains a pull of the Panel registry's own hostname that
// could not reach it: the host must resolve and reach itself (ADR-0033).
func (s *Service) hairpinHint(ctx context.Context, err error) string {
	if s.registryHost == nil {
		return ""
	}
	host, ok := s.registryHost(ctx)
	msg := strings.ToLower(err.Error())
	if !ok || !strings.Contains(msg, host) || !unreachable.MatchString(msg) {
		return ""
	}
	return "This host could not reach its own Registry at " + host + ". Check that the hostname resolves to this host from the host itself."
}

// imageHost is the registry host of an image reference, as Docker reads it:
// a first path part with a dot or a port, or localhost, names the host;
// anything else is Docker Hub.
func imageHost(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	first, _, found := strings.Cut(ref, "/")
	if !found || !(strings.ContainsAny(first, ".:") || first == "localhost") {
		return "docker.io"
	}
	switch host := strings.ToLower(first); host {
	case "index.docker.io", "registry-1.docker.io":
		return "docker.io"
	default:
		return host
	}
}

const volumesRemovedAdvice = "The Compose is stopped; fix the cause and deploy again."

func volumesAdvice(fresh bool) string {
	if fresh {
		return ". " + volumesRemovedAdvice
	}
	return ""
}

// BuildPlan is what building a Compose's Services needs on disk.
type BuildPlan struct {
	Request  RunRequest // ProjectName as deploy; ComposeFilePath .build-compose.yml; EnvFilePath .build.env
	Services []string   // Services with build:, in YAML order
	Cleanup  func()     // removes both staging files
}

// PrepareBuild writes the stored YAML, unresolved apart from declaring the
// shared network external, and the merged Environment
// and Compose Variables next to the Compose's files, so a build can run before
// the first deploy has written the resolved ones.
func (s *Service) PrepareBuild(ctx context.Context, t DeployTarget) (BuildPlan, error) {
	if t.Snapshot != nil {
		return prepareSnapshotBuild(t)
	}
	services, err := buildServices(t.Compose.YAMLContent)
	if err != nil {
		return BuildPlan{}, fmt.Errorf("compose yaml is invalid: %w", err)
	}
	vars, err := s.repository.loadDeployVariables(ctx, t.Compose, true)
	if err != nil {
		return BuildPlan{}, fmt.Errorf("could not load variables: %w", err)
	}
	dir := ComposeDir(t.Request)
	plan := BuildPlan{
		Request: RunRequest{
			ProjectName:     t.Request.ProjectName,
			ComposeFilePath: filepath.Join(dir, buildComposeFileName),
			EnvFilePath:     filepath.Join(dir, buildEnvFileName),
		},
		Services: services,
	}
	plan.Cleanup = func() {
		os.Remove(plan.Request.ComposeFilePath)
		os.Remove(plan.Request.EnvFilePath)
	}
	if err := os.MkdirAll(dir, composeDirPerm); err != nil {
		return BuildPlan{}, fmt.Errorf("could not prepare compose files: %w", err)
	}
	envFile := renderEnvFile(mergeVariables(vars.Environment, vars.Compose, nil).Interpolated)
	if err := os.WriteFile(plan.Request.EnvFilePath, []byte(envFile), envFilePerm); err != nil {
		plan.Cleanup()
		return BuildPlan{}, fmt.Errorf("could not write build environment file: %w", err)
	}
	staged, err := declareSharedNetwork(t.Compose.YAMLContent, dockernet.NetworkName)
	if err != nil {
		plan.Cleanup()
		return BuildPlan{}, fmt.Errorf("compose yaml is invalid: %w", err)
	}
	if err := os.WriteFile(plan.Request.ComposeFilePath, []byte(staged), composeFilePerm); err != nil {
		plan.Cleanup()
		return BuildPlan{}, fmt.Errorf("could not write build compose file: %w", err)
	}
	return plan, nil
}

// buildServices lists the Services that declare build:, in YAML order. The
// set comes from a map decode, which resolves merge keys and aliases; the order
// comes from expanding the effective root and Services mappings as YAML nodes.
func buildServices(yamlContent string) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || resolveAlias(doc.Content[0]).Kind != yaml.MappingNode {
		return nil, nil
	}
	var decoded struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(yamlContent), &decoded); err != nil {
		return nil, err
	}
	hasBuild := map[string]bool{}
	for name, body := range decoded.Services {
		if m, ok := body.(map[string]any); ok {
			if _, ok := m["build"]; ok {
				hasBuild[name] = true
			}
		}
	}
	var names []string
	root, err := orderedMappingEntries(doc.Content[0])
	if err != nil {
		return nil, err
	}
	for _, entry := range root {
		if resolveAlias(entry.key).Value != "services" {
			continue
		}
		services, err := orderedMappingEntries(entry.value)
		if err != nil {
			return nil, err
		}
		for _, service := range services {
			if name := resolveAlias(service.key).Value; hasBuild[name] {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

type yamlMappingEntry struct {
	key, value *yaml.Node
}

// orderedMappingEntries expands merges in place while keeping explicit keys at
// their own positions. Explicit keys override every merged map; earlier maps in
// a merge sequence override later ones. Callers validate through yaml.Decode
// first so that invalid merge values and duplicate keys keep YAML's own errors.
func orderedMappingEntries(node *yaml.Node) ([]yamlMappingEntry, error) {
	active := map[*yaml.Node]bool{}
	var expand func(*yaml.Node) ([]yamlMappingEntry, error)
	expand = func(node *yaml.Node) ([]yamlMappingEntry, error) {
		node = resolveAlias(node)
		if node == nil || node.Kind != yaml.MappingNode {
			return nil, nil
		}
		if active[node] {
			return nil, fmt.Errorf("yaml: recursive mapping merge")
		}
		active[node] = true
		defer delete(active, node)

		explicit := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if key := node.Content[i]; !isYAMLMergeKey(key) {
				explicit[resolveAlias(key).Value] = true
			}
		}
		var entries []yamlMappingEntry
		merged := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if !isYAMLMergeKey(key) {
				entries = append(entries, yamlMappingEntry{key, value})
				continue
			}
			sources := []*yaml.Node{value}
			if value.Kind == yaml.SequenceNode {
				sources = value.Content
			}
			for _, source := range sources {
				inherited, err := expand(source)
				if err != nil {
					return nil, err
				}
				for _, entry := range inherited {
					name := resolveAlias(entry.key).Value
					if !explicit[name] && !merged[name] {
						entries = append(entries, entry)
						merged[name] = true
					}
				}
			}
		}
		return entries, nil
	}
	return expand(node)
}

// isYAMLMergeKey follows yaml.v3: aliases of "<<" are ordinary mapping keys.
func isYAMLMergeKey(key *yaml.Node) bool {
	return key.Kind == yaml.ScalarNode && key.Value == "<<" && key.ShortTag() == "!!merge"
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// declareSharedNetwork adds networks.<name>: {external: true} to the stored
// YAML when it is not declared, so a Service that lists the shared network
// (the documented pattern) passes docker compose's validation during a build.
// A YAML that already declares it is returned unchanged.
func declareSharedNetwork(yamlContent, name string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return "", err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return yamlContent, nil
	}
	var decoded map[string]any
	if err := doc.Decode(&decoded); err != nil {
		return "", err
	}
	root := doc.Content[0]
	entries, err := orderedMappingEntries(root)
	if err != nil {
		return "", err
	}
	var existing *yaml.Node
	for _, entry := range entries {
		if resolveAlias(entry.key).Value == "networks" {
			existing = entry.value
			break
		}
	}
	if existing != nil {
		switch resolveAlias(existing).Kind {
		case yaml.MappingNode:
			networks, err := orderedMappingEntries(existing)
			if err != nil {
				return "", err
			}
			for _, entry := range networks {
				if resolveAlias(entry.key).Value == name {
					return yamlContent, nil
				}
			}
		case yaml.ScalarNode: // `networks:` with a null value
			existing = nil
		default:
			return yamlContent, nil
		}
	}
	explicitIndex := -1
	for i := 0; i+1 < len(root.Content); i += 2 {
		if resolveAlias(root.Content[i]).Value == "networks" {
			explicitIndex = i + 1
			break
		}
	}
	declaration := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "external"},
		{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
	}}
	networks := &yaml.Node{Kind: yaml.MappingNode}
	if existing != nil {
		// Explicit declarations stay at their original position, preserving
		// anchors used elsewhere. Inherited declarations need a resolved copy:
		// moving an alias to the end could bind it to a later reused anchor name.
		// Their original definitions remain intact in the inherited mapping.
		if explicitIndex == -1 {
			existing = &yaml.Node{}
			if err := existing.Encode(decoded["networks"]); err != nil {
				return "", err
			}
		}
		networks.Content = append(networks.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "<<"}, existing)
	}
	networks.Content = append(networks.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, declaration)
	if explicitIndex != -1 {
		root.Content[explicitIndex] = networks
	} else {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "networks"}, networks)
	}
	out, err := yaml.Marshal(&doc)
	return string(out), err
}

// deploySubset runs up for every active Service but the excluded ones, or,
// when none is left, removes orphans itself.
func (s *Service) deploySubset(ctx context.Context, req RunRequest, yamlContent string, opts DeployOptions) error {
	active, err := s.activeServices(ctx, req, yamlContent)
	if err != nil {
		return err
	}
	excluded := map[string]bool{}
	for _, name := range opts.Exclude {
		excluded[name] = true
	}
	var rest []string
	for _, name := range active {
		if !excluded[name] {
			rest = append(rest, name)
		}
	}
	if len(rest) == 0 {
		return s.removeOrphans(ctx, req.ProjectName, yamlContent)
	}
	return s.runner.Up(ctx, req, UpOptions{NoPull: true, Services: rest, Out: opts.Out})
}

// prepareImages builds (when build) and pulls every image the deployed file
// runs, then refuses one that brings its own Traefik labels onto a container
// Traefik reads (policy.ImageLabels, ADR-0032): the Compose policy only sees
// the Compose's labels. Every up after it runs with --no-build and --pull
// never, so the images it checked are the images that start. It returns
// those images, sorted, for compose_image_refs.
func (s *Service) prepareImages(ctx context.Context, req RunRequest, build bool, out io.Writer) ([]string, error) {
	raw, err := s.runner.Config(ctx, req)
	if err != nil {
		return nil, failure(FailureRunner, "could not read compose config: "+err.Error(), err)
	}
	var config struct {
		Name     string `json:"name"`
		Services map[string]struct {
			Image      string            `json:"image"`
			Build      any               `json:"build"`
			PullPolicy string            `json:"pull_policy"`
			Platform   string            `json:"platform"`
			Labels     map[string]string `json:"labels"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, failure(FailureInternal, "could not read compose config", err)
	}
	project := config.Name
	if project == "" {
		project = req.ProjectName
	}
	names := make([]string, 0, len(config.Services))
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	var missing, refresh []string
	builds := false
	for _, name := range names {
		service := config.Services[name]
		if service.Build != nil {
			builds = true
			continue
		}
		switch pull := service.PullPolicy; {
		case pull == "never":
		case pull == "" || pull == "missing" || pull == "if_not_present":
			missing = append(missing, name)
		default: // always, daily, weekly, every_<duration>
			refresh = append(refresh, name)
		}
	}
	if build && builds {
		if err := s.runner.Build(ctx, req, BuildOptions{Out: out}); err != nil {
			return nil, failure(FailureRunner, "build failed: "+err.Error(), err)
		}
	}
	for _, pull := range []PullOptions{{Missing: true, Services: missing, Out: out}, {Services: refresh, Out: out}} {
		if len(pull.Services) == 0 {
			continue
		}
		if err := s.runner.Pull(ctx, req, pull); err != nil {
			images := make([]string, 0, len(pull.Services))
			for _, name := range pull.Services {
				images = append(images, config.Services[name].Image)
			}
			msg := "pull failed: " + err.Error()
			// The hint goes first: a Deployment's reason is the message's
			// first line, and compose prints the refusal lines later.
			if hint := s.pullHint(ctx, err, images); hint != "" {
				msg = "pull failed: " + hint + " " + err.Error()
			}
			return nil, failure(FailureRunner, msg, err)
		}
	}
	var refused, refs []string
	for _, name := range names {
		service := config.Services[name]
		image := service.Image
		if image == "" {
			image = project + "-" + name // Compose's name for a built image
		}
		refs = append(refs, image)
		// One tag can hold another image per platform: read the one this
		// Service runs.
		labels, err := s.runner.ImageLabels(ctx, image, service.Platform)
		if err != nil {
			return nil, failure(FailureRunner, "could not inspect image "+image+": "+err.Error(), err)
		}
		for _, v := range policy.ImageLabels(name, image, labels, service.Labels) {
			refused = append(refused, v.Service+": "+v.Message)
		}
	}
	if len(refused) > 0 {
		return nil, failure(FailureInvalid, "policy: "+strings.Join(refused, "; "), nil)
	}
	return sortedUnique(refs), nil
}
