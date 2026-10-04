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
