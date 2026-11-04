package composes

import (
	"reflect"
	"testing"
)

func TestServiceWithoutLoggingGetsRotatedJSONFile(t *testing.T) {
	services, _ := resolveServices(t, "services:\n  web: {image: nginx}\n", nil, testIdentity)
	want := map[string]any{
		"driver":  "json-file",
		"options": map[string]any{"max-size": "10m", "max-file": "5"},
	}
	if !reflect.DeepEqual(services["web"]["logging"], want) {
		t.Fatalf("logging = %#v, want %#v", services["web"]["logging"], want)
	}
}

func TestServiceOwnLoggingIsKeptAsWritten(t *testing.T) {
	const yamlContent = "services:\n" +
		"  web:\n    image: nginx\n    logging: {driver: local}\n" +
		"  db:\n    image: postgres\n    logging: {driver: json-file}\n"
	services, _ := resolveServices(t, yamlContent, nil, testIdentity)
	if got := services["web"]["logging"]; !reflect.DeepEqual(got, map[string]any{"driver": "local"}) {
		t.Fatalf("web logging = %#v, want driver local only", got)
	}
	if got := services["db"]["logging"]; !reflect.DeepEqual(got, map[string]any{"driver": "json-file"}) {
		t.Fatalf("db logging = %#v, want driver json-file with no options added", got)
	}
}
