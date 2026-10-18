package composes

import "strings"

func (v deployVariables) secretValues() []string {
	var values []string
	appendValues := func(vars []environmentVariable) {
		for _, variable := range vars {
			if variable.Secret && variable.Value != "" {
				values = append(values, variable.Value)
			}
		}
	}
	appendValues(v.Environment)
	appendValues(v.Compose)
	for _, binding := range v.Bindings {
		appendValues(binding.Set.Entries)
	}
	return values
}

func (v deployVariables) interpolationValues() map[string]string {
	values := map[string]string{}
	for _, variable := range mergeVariables(v.Environment, v.Compose, nil).Interpolated {
		values[variable.Key] = variable.Value
	}
	return values
}

// expandPath only resolves path fields, with Compose's variable/default forms.
// Other YAML still goes through Docker's ordinary interpolation from saved .env.
func expandPath(path string, values map[string]string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(path); {
		if path[i] != '$' {
			out.WriteByte(path[i])
			i++
			continue
		}
		i++
		if i >= len(path) {
			out.WriteByte('$')
			break
		}
		if path[i] == '$' {
			out.WriteByte('$')
			i++
			continue
		}
		if path[i] != '{' {
			start := i
			for i < len(path) && isVariableByte(path[i]) {
				i++
			}
			if start == i {
				return path, false
			}
			out.WriteString(values[path[start:i]])
			continue
		}
		i++
		start := i
		depth := 1
		for i < len(path) && depth > 0 {
			if path[i] == '{' {
				depth++
			}
			if path[i] == '}' {
				depth--
			}
			if depth > 0 {
				i++
			}
		}
		if depth != 0 {
			return path, false
		}
		body := path[start:i]
		i++
		end := 0
		for end < len(body) && isVariableByte(body[end]) {
			end++
		}
		if end == 0 {
			return path, false
		}
		value, set := values[body[:end]]
		expression := body[end:]
		if expression != "" {
			op := ""
			for _, candidate := range []string{":-", ":+", ":?", "-", "+", "?"} {
				if strings.HasPrefix(expression, candidate) {
					op = candidate
					break
				}
			}
			if op == "" {
				return path, false
			}
			argument := strings.TrimPrefix(expression, op)
			useArgument := false
			switch op {
			case ":-":
				useArgument = !set || value == ""
			case "-":
				useArgument = !set
			case ":+":
				useArgument = set && value != ""
				if !useArgument {
					value = ""
				}
			case "+":
				useArgument = set
				if !useArgument {
					value = ""
				}
			case ":?":
				if !set || value == "" {
					return path, false
				}
			case "?":
				if !set {
					return path, false
				}
			}
			if useArgument {
				var ok bool
				value, ok = expandPath(argument, values)
				if !ok {
					return path, false
				}
			}
		}
		out.WriteString(value)
	}
	return out.String(), true
}

func isVariableByte(b byte) bool {
	return b == '_' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}
