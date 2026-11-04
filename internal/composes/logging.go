package composes

// withDefaultLogging caps a Service's Docker log when its YAML does not say
// how to log (ADR-0028). Docker's json-file driver never rotates on its own,
// so a long-running Container would otherwise fill the disk. A Service that
// writes its own logging: is left exactly as written.
func withDefaultLogging(service map[string]any) {
	if _, ok := service["logging"]; ok {
		return
	}
	service["logging"] = map[string]any{
		"driver":  "json-file",
		"options": map[string]any{"max-size": "10m", "max-file": "5"},
	}
}
