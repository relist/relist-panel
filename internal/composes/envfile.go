package composes

import "strings"

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

// mergeVariables is the Compose's interpolated variable list: a Compose
// Variable replaces the Environment Variable with the same key. Environment
// keys keep their order and Compose-only keys follow in the order given, so
// no key appears twice. Both inputs are expected in ID order. It is pure so
// the deploy and any view of the result share one set of rules.
func mergeVariables(environment, compose []environmentVariable) []environmentVariable {
	override := make(map[string]string, len(compose))
	for _, variable := range compose {
		override[variable.Key] = variable.Value
	}
	merged := make([]environmentVariable, 0, len(environment)+len(compose))
	seen := make(map[string]bool, len(environment))
	for _, variable := range environment {
		if value, ok := override[variable.Key]; ok {
			variable.Value = value
		}
		seen[variable.Key] = true
		merged = append(merged, variable)
	}
	for _, variable := range compose {
		if !seen[variable.Key] {
			seen[variable.Key] = true
			merged = append(merged, variable)
		}
	}
	return merged
}
