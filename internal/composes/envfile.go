package composes

import (
	"encoding/json"
	"strings"
)

// renderEnvFile writes literal variable values in Compose's dotenv grammar.
// Double quotes retain whitespace and #; escaping prevents values from
// becoming interpolation expressions or additional environment assignments.
// Use explicit control escapes so CR/LF normalization cannot change a value.
func renderEnvFile(variables []environmentVariable) string {
	escape := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"$", "$$",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	)
	var b strings.Builder
	for _, variable := range variables {
		b.WriteString(variable.Key)
		b.WriteString("=\"")
		b.WriteString(escape.Replace(variable.Value))
		b.WriteString("\"\n")
	}
	return b.String()
}

// boundSet is a Variable Set as one Service sees it: its entries in ID order.
type boundSet struct {
	ID      int64
	Name    string
	Entries []environmentVariable
}

// variableSource names the layer a value came from. Kind is "environment",
// "compose", "set" or "yaml"; SetID and SetName are only meaningful for "set".
type variableSource struct {
	Kind    string
	SetID   int64
	SetName string
}

// MarshalJSON writes a plain string for every layer but a set, which is
// {"set": {"id": ..., "name": ...}}.
func (s variableSource) MarshalJSON() ([]byte, error) {
	if s.Kind != "set" {
		return json.Marshal(s.Kind)
	}
	type setRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	return json.Marshal(map[string]setRef{"set": {ID: s.SetID, Name: s.SetName}})
}

// effectiveVariable is the final value of one key and the layers it shadowed,
// lowest first.
type effectiveVariable struct {
	Key       string           `json:"key"`
	Value     string           `json:"value"`
	Secret    bool             `json:"secret"`
	Source    variableSource   `json:"source"`
	Overrides []variableSource `json:"overrides"`
}

// mergedVariables is the result of layering the variables a Service sees.
type mergedVariables struct {
	// Interpolated is the Compose's .env: Environment Variables overridden by
	// Compose Variables. Environment keys keep their order and Compose-only
	// keys follow in the order given, so no key appears twice.
	Interpolated []environmentVariable
	// Effective is what one container ends up with once the bound sets are
	// applied on top (a later set wins over an earlier one): the .env keys in
	// their order, then set-only keys in first-seen order. It does not include
	// the YAML's own env_file and environment, which are not resolved here.
	Effective []effectiveVariable
}

// mergeVariables is the one set of precedence rules (ADR-0015) used by both
// the deploy and the effective-variables view: environment, then compose,
// then sets in the order given. Both variable inputs are expected in ID order.
// It is pure so nothing else re-derives precedence.
func mergeVariables(environment, compose []environmentVariable, sets []boundSet) mergedVariables {
	var merged mergedVariables
	position := make(map[string]int, len(environment)+len(compose))
	put := func(variable environmentVariable, source variableSource) {
		index, seen := position[variable.Key]
		if !seen {
			position[variable.Key] = len(merged.Effective)
			merged.Effective = append(merged.Effective, effectiveVariable{
				Key: variable.Key, Value: variable.Value, Secret: variable.Secret, Source: source, Overrides: []variableSource{},
			})
			return
		}
		current := &merged.Effective[index]
		current.Overrides = append(current.Overrides, current.Source)
		current.Value, current.Secret, current.Source = variable.Value, variable.Secret, source
	}
	for _, variable := range environment {
		put(variable, variableSource{Kind: "environment"})
	}
	for _, variable := range compose {
		put(variable, variableSource{Kind: "compose"})
	}
	merged.Interpolated = make([]environmentVariable, 0, len(merged.Effective))
	for _, variable := range merged.Effective {
		merged.Interpolated = append(merged.Interpolated, environmentVariable{Key: variable.Key, Value: variable.Value, Secret: variable.Secret})
	}
	for _, set := range sets {
		for _, variable := range set.Entries {
			put(variable, variableSource{Kind: "set", SetID: set.ID, SetName: set.Name})
		}
	}
	return merged
}
