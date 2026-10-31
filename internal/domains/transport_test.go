package domains_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/domains"
)

func TestWriteTransportFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dynamic")
	for range 2 {
		if err := domains.WriteTransportFile(dir); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, domains.TransportFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HTTP struct {
			ServersTransports map[string]struct {
				ForwardingTimeouts struct {
					DialTimeout string `yaml:"dialTimeout"`
				} `yaml:"forwardingTimeouts"`
			} `yaml:"serversTransports"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if dial := got.HTTP.ServersTransports["relist-fastdial"].ForwardingTimeouts.DialTimeout; dial != "1s" {
		t.Fatalf("dialTimeout = %q, want 1s\n%s", dial, raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want only the transport file", len(entries))
	}
}
