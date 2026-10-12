package composes

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeclareSharedNetworkResolvesMappingKeyAliases(t *testing.T) {
	cases := []struct {
		name      string
		yaml      string
		unchanged bool
	}{
		{
			name: "explicit root network key alias",
			yaml: "x-key: &key networks\n*key:\n  backend:\n    name: custom-backend\nservices:\n  api:\n    build: .\n    networks: [backend]\n",
		},
		{
			name: "inherited root network key alias",
			yaml: "x-key: &key networks\nx-defaults: &defaults\n  *key:\n    backend:\n      name: custom-backend\n<<: *defaults\nservices:\n  api:\n    build: .\n    networks: [backend]\n",
		},
		{
			name: "inherited mapping before an anchor name is reused",
			yaml: "x-defaults: &defaults\n  networks: &network-map\n    backend:\n      name: custom-backend\n<<: *defaults\nx-later: &network-map\n  different: {}\nservices:\n  api:\n    build: .\n    networks: [backend]\n",
		},
		{
			name: "inherited alias before an anchor name is reused",
			yaml: "x-networks: &network-map\n  backend:\n    name: custom-backend\nx-defaults: &defaults\n  networks: *network-map\n<<: *defaults\nx-later: &network-map\n  different: {}\nservices:\n  api:\n    build: .\n    networks: [backend]\n",
		},
		{
			name:      "existing shared network key alias",
			yaml:      "x-key: &key relist-network\nnetworks:\n  *key:\n    external: true\n    name: custom-shared\nservices:\n  api:\n    build: .\n",
			unchanged: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := declareSharedNetwork(c.yaml, "relist-network")
			if err != nil {
				t.Fatal(err)
			}
			if c.unchanged {
				if got != c.yaml {
					t.Fatalf("existing shared network changed:\n%s", got)
				}
				return
			}
			var decoded struct {
				Networks map[string]map[string]any `yaml:"networks"`
			}
			if err := yaml.Unmarshal([]byte(got), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Networks["backend"]["name"] != "custom-backend" {
				t.Fatalf("backend network options lost:\n%s", got)
			}
			if decoded.Networks["relist-network"]["external"] != true {
				t.Fatalf("shared network not declared external:\n%s", got)
			}
		})
	}
}
