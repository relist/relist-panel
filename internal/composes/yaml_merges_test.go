package composes

import (
	"reflect"
	"testing"
)

func TestBuildServicesResolvesMappingKeyAliases(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "root key alias",
			yaml: "x-key: &key services\n*key:\n  api:\n    build: .\n",
			want: []string{"api"},
		},
		{
			name: "service key alias",
			yaml: "x-name: &name api\nservices:\n  *name:\n    build: .\n",
			want: []string{"api"},
		},
		{
			name: "root key alias inherited through merge",
			yaml: "x-key: &key services\nx-defaults: &defaults\n  *key:\n    api:\n      build: .\n<<: *defaults\n",
			want: []string{"api"},
		},
		{
			name: "inherited service key alias",
			yaml: "x-name: &name api\nx-services: &services\n  *name:\n    build: .\nservices:\n  <<: *services\n  worker:\n    build: .\n",
			want: []string{"api", "worker"},
		},
		{
			name: "explicit alias keeps its own position after merge",
			yaml: "x-name: &name api\nx-services: &services\n  api:\n    image: busybox\nservices:\n  <<: *services\n  before:\n    build: .\n  *name:\n    build: .\n  after:\n    build: .\n",
			want: []string{"before", "api", "after"},
		},
		{
			name: "explicit literal overrides inherited alias at its own position",
			yaml: "x-name: &name api\nx-services: &services\n  *name:\n    build: ./ignored\nservices:\n  <<: *services\n  before:\n    build: .\n  api:\n    build: .\n",
			want: []string{"before", "api"},
		},
		{
			name: "first map wins over alias key in later map",
			yaml: "x-name: &name api\nx-first: &first\n  api:\n    build: .\nx-second: &second\n  *name:\n    build: ./ignored\n  worker:\n    build: .\nservices:\n  <<: [*first, *second]\n",
			want: []string{"api", "worker"},
		},
		{
			name: "first alias key without build wins over later literal key",
			yaml: "x-name: &name api\nx-first: &first\n  *name:\n    image: busybox\nx-second: &second\n  api:\n    build: ./ignored\n  worker:\n    build: .\nservices:\n  <<: [*first, *second]\n",
			want: []string{"worker"},
		},
		{
			name: "alias of quoted merge-looking key is ordinary",
			yaml: "x-name: &name '<<'\nservices:\n  *name:\n    build: .\n  api:\n    build: .\n",
			want: []string{"<<", "api"},
		},
		{
			name: "alias of merge-tagged scalar key is ordinary",
			yaml: "x-name: &name <<\nservices:\n  *name:\n    build: .\n  api:\n    build: .\n",
			want: []string{"<<", "api"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := buildServices(c.yaml)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("build services = %v, want %v", got, c.want)
			}
		})
	}
}
