package composes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/audit"
	"relist-panel/internal/authz"
)

const setsDirName = "sets"

// setBinding is one Variable Set bound to one Service. Bindings are kept in
// Service name order, then binding position.
type setBinding struct {
	Service string
	Set     boundSet
}

// deployVariables is every variable input of a deploy. When loaded without
// values (preview) the Value of every variable is empty; the keys are still
// there for the warnings.
type deployVariables struct {
	Environment []environmentVariable
	Compose     []environmentVariable
	Bindings    []setBinding
}

// setsFor is the sets bound to one Service, in binding order.
func (v deployVariables) setsFor(service string) []boundSet {
	var sets []boundSet
	for _, binding := range v.Bindings {
		if binding.Service == service {
			sets = append(sets, binding.Set)
		}
	}
	return sets
}

func setsDir(envFilePath string) string {
	return filepath.Join(filepath.Dir(envFilePath), setsDirName)
}

func setEnvPath(envFilePath string, setID int64) string {
	return filepath.Join(setsDir(envFilePath), strconv.FormatInt(setID, 10)+".env")
}

// loadDeployVariables reads the Environment Variables, Compose Variables and
// bindings of a Compose. Without withValues no value is read from the database.
func (r *repository) loadDeployVariables(ctx context.Context, compose Compose, withValues bool) (deployVariables, error) {
	var vars deployVariables
	var err error
	if vars.Environment, err = r.listEnvironmentVariables(ctx, compose.EnvironmentID, withValues); err != nil {
		return deployVariables{}, err
	}
	if vars.Compose, err = r.listComposeVariables(ctx, compose.ID, withValues); err != nil {
		return deployVariables{}, err
	}
	vars.Bindings, err = r.listBindings(ctx, compose.ID, withValues)
	return vars, err
}

func (r *repository) listBindings(ctx context.Context, composeID int64, withValues bool) ([]setBinding, error) {
	valueColumn := "''"
	if withValues {
		valueColumn = "e.value"
	}
	// valueColumn is a constant from this function.
	rows, err := r.db.QueryContext(ctx, `
		SELECT b.service, b.set_id, s.name, e.key, `+valueColumn+`, e.secret
		FROM service_bindings b
		JOIN variable_sets s ON s.id = b.set_id
		LEFT JOIN variable_set_entries e ON e.set_id = s.id
		WHERE b.compose_id = ?
		ORDER BY b.service, b.position, e.id
	`, composeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bindings []setBinding
	for rows.Next() {
		var (
			service, name string
			setID         int64
			key, value    sql.NullString
			secret        sql.NullBool
		)
		if err := rows.Scan(&service, &setID, &name, &key, &value, &secret); err != nil {
			return nil, err
		}
		if n := len(bindings); n == 0 || bindings[n-1].Service != service || bindings[n-1].Set.ID != setID {
			bindings = append(bindings, setBinding{Service: service, Set: boundSet{ID: setID, Name: name}})
		}
		if key.Valid {
			last := &bindings[len(bindings)-1].Set
			last.Entries = append(last.Entries, environmentVariable{Key: key.String, Value: value.String, Secret: secret.Bool})
		}
	}
	return bindings, rows.Err()
}

// setFiles renders one env file per set in ids, keyed by set ID.
func (v deployVariables) setFiles(ids []int64) map[int64]string {
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	files := make(map[int64]string, len(ids))
	for _, binding := range v.Bindings {
		if wanted[binding.Set.ID] {
			files[binding.Set.ID] = renderEnvFile(binding.Set.Entries)
		}
	}
	return files
}

// addSetFiles appends the env files of a Service's bound sets, in order, after
// whatever env_file already holds.
func addSetFiles(existing any, paths []string) any {
	if len(paths) == 0 {
		return existing
	}
	sequence, ok := existing.([]any)
	if !ok {
		sequence = []any{existing}
	}
	for _, path := range paths {
		sequence = appendUnique(sequence, path)
	}
	return sequence
}

// declaredService reports whether services has a Service of that name whose
// definition resolveCompose can inject into.
func declaredService(services map[string]any, name string) bool {
	_, ok := services[name].(map[string]any)
	return ok
}

// variableWarnings are the operator-facing notes about the Compose's bindings
// and its use of variables. They read keys only. services is the YAML's
// Services as written.
func variableWarnings(yamlContent string, services map[string]any, vars deployVariables) []string {
	var warnings []string

	// A binding to a Service the YAML does not declare.
	for _, binding := range vars.Bindings {
		if !declaredService(services, binding.Service) {
			warnings = append(warnings, fmt.Sprintf("variable set %q is bound to service %q, which the compose does not declare", binding.Set.Name, binding.Service))
		}
	}

	// Two sets bound to one Service that share a key.
	holder := map[string]string{}
	var holderService string
	for _, binding := range vars.Bindings {
		if binding.Service != holderService {
			holder, holderService = map[string]string{}, binding.Service
		}
		if !declaredService(services, binding.Service) {
			continue
		}
		for _, entry := range binding.Set.Entries {
			if earlier, ok := holder[entry.Key]; ok {
				warnings = append(warnings, fmt.Sprintf("variable sets %q and %q both set %q for service %q; %q wins", earlier, binding.Set.Name, entry.Key, binding.Service, binding.Set.Name))
			}
			holder[entry.Key] = binding.Set.Name
		}
	}

	// ${NAME} that only a set holds. Sets are not used for interpolation. A key
	// in several sets is reported against the first by name.
	if len(vars.Bindings) > 0 {
		interpolated := make(map[string]bool, len(vars.Environment)+len(vars.Compose))
		for _, variable := range append(append([]environmentVariable{}, vars.Environment...), vars.Compose...) {
			interpolated[variable.Key] = true
		}
		inSet := map[string]string{}
		for _, binding := range vars.Bindings {
			for _, entry := range binding.Set.Entries {
				if first, ok := inSet[entry.Key]; !ok || binding.Set.Name < first {
					inSet[entry.Key] = binding.Set.Name
				}
			}
		}
		reported := map[string]bool{}
		for _, name := range interpolationNames(yamlContent) {
			if setName, ok := inSet[name]; ok && !interpolated[name] && !reported[name] {
				reported[name] = true
				warnings = append(warnings, fmt.Sprintf("${%s} is only in variable set %q, which is not used for ${...} interpolation", name, setName))
			}
		}
	}

	// A bare environment: key resolves from the .env only, so a value that only
	// a bound set holds is lost.
	plain := make(map[string]bool, len(vars.Environment)+len(vars.Compose))
	for _, variable := range append(append([]environmentVariable{}, vars.Environment...), vars.Compose...) {
		plain[variable.Key] = true
	}
	serviceNames := make([]string, 0, len(services))
	for name := range services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		if !declaredService(services, name) {
			continue
		}
		inSet := map[string]string{}
		for _, set := range vars.setsFor(name) {
			for _, entry := range set.Entries {
				inSet[entry.Key] = set.Name
			}
		}
		if len(inSet) == 0 {
			continue
		}
		for _, key := range yamlBareEnvironmentKeys(services[name].(map[string]any)["environment"]) {
			if setName, ok := inSet[key]; ok && !plain[key] {
				warnings = append(warnings, fmt.Sprintf("service %q lists %s in environment: without a value, so Compose resolves it from the .env and the value from variable set %q is lost", name, key, setName))
			}
		}
	}
	return warnings
}

// yamlBareEnvironmentKeys lists the keys of an environment: block that have no
// value: a list entry without "=" or a map key with a null value. Sorted.
func yamlBareEnvironmentKeys(environment any) []string {
	var keys []string
	switch environment := environment.(type) {
	case map[string]any:
		for key, value := range environment {
			if value == nil {
				keys = append(keys, key)
			}
		}
	case []any:
		for _, item := range environment {
			if entry, ok := item.(string); ok && !strings.Contains(entry, "=") {
				keys = append(keys, entry)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// interpolation matches Compose's $$ escape, ${NAME...} and $NAME, in order.
var interpolation = regexp.MustCompile(`\$(?:\$|\{([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*))`)

// interpolationNames lists the variable names the YAML asks Compose to
// interpolate, in order of appearance.
func interpolationNames(yamlContent string) []string {
	var names []string
	for _, match := range interpolation.FindAllStringSubmatch(yamlContent, -1) {
		if match[1] != "" {
			names = append(names, match[1])
		} else if match[2] != "" {
			names = append(names, match[2])
		}
	}
	return names
}

// yamlEnvironmentKeys lists the keys of a Service's own environment: block in
// either of its forms, sorted. It resolves nothing.
func yamlEnvironmentKeys(yamlContent, service string) []string {
	var doc struct {
		Services map[string]struct {
			Environment any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return nil
	}
	var keys []string
	switch environment := doc.Services[service].Environment.(type) {
	case map[string]any:
		for key := range environment {
			keys = append(keys, key)
		}
	case []any:
		for _, item := range environment {
			if entry, ok := item.(string); ok {
				key, _, _ := strings.Cut(entry, "=")
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// effectiveVariables answers what one Service's containers will get at the
// next deploy, from the same merge the deploy uses. The YAML's own
// environment: keys follow as source "yaml" with no value: they win over
// everything here but are not resolved.
func (s *Service) effectiveVariables(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}
	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	service := r.PathValue("service")
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}
	compose, err := s.repository.findByID(r.Context(), environmentID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	vars, err := s.repository.loadDeployVariables(r.Context(), compose, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load variables")
		return
	}
	effective := mergeVariables(vars.Environment, vars.Compose, vars.setsFor(service)).Effective
	yamlKeys := yamlEnvironmentKeys(compose.YAMLContent, service)
	inYAML := make(map[string]bool, len(yamlKeys))
	for _, key := range yamlKeys {
		inYAML[key] = true
	}
	for i := range effective {
		if inYAML[effective[i].Key] {
			effective[i].Overrides = append(effective[i].Overrides, variableSource{Kind: "yaml"})
		}
	}
	for _, key := range yamlKeys {
		effective = append(effective, effectiveVariable{Key: key, Source: variableSource{Kind: "yaml"}, Overrides: []variableSource{}})
	}
	if !authz.CanReadSecrets(r.Context(), environmentID) {
		for i := range effective {
			if effective[i].Secret {
				effective[i].Value = ""
			}
		}
	} else {
		var keys []string
		for _, variable := range effective {
			if variable.Secret && variable.Value != "" {
				keys = append(keys, variable.Key)
			}
		}
		audit.SecretRead(r.Context(), keys)
	}
	writeJSON(w, http.StatusOK, effective)
}
