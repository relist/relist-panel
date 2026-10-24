package composes

import (
	"reflect"
	"testing"
)

const (
	srcWarning    = `Service "web" mounts ./src writable at /app: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.`
	volumeWarning = `Volume "data" sets name: shared-db without external: true, so deleting this Compose removes it. Declare it external: true if other stacks use it.`
)

func TestVolumeWarnings(t *testing.T) {
	const dir = "/data/.composes/4"
	cases := []struct {
		name       string
		yaml       string
		hasSource  bool
		elsewhere  map[string]string
		wantExact  []string
		wantAbsent bool
	}{
		{name: "writable ./src bind warns", hasSource: true, wantExact: []string{srcWarning},
			yaml: "services:\n  web:\n    image: nginx\n    volumes:\n      - ./src:/app\n"},
		{name: "writable subdirectory warns with the path as written", hasSource: true,
			wantExact: []string{`Service "web" mounts ./src/data writable at /app: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.`},
			yaml:      "services:\n  web:\n    image: nginx\n    volumes:\n      - ./src/data:/app:rw\n"},
		{name: "read-only short bind", hasSource: true,
			yaml: "services:\n  web:\n    image: nginx\n    volumes:\n      - ./src:/app:ro\n      - ./src/conf:/conf:ro,z\n"},
		{name: "read-only long bind", hasSource: true,
			yaml: "services:\n  web:\n    image: nginx\n    volumes:\n      - {type: bind, source: ./src/data, target: /data, read_only: true}\n"},
		{name: "bind outside src", hasSource: true,
			yaml: "services:\n  web:\n    image: nginx\n    volumes:\n      - ./data:/data\n      - ./srcs:/x\n      - ../src:/y\n"},
		{name: "named volume called src is not a bind", hasSource: true,
			yaml: "services:\n  web:\n    image: nginx\n    volumes:\n      - src:/app\n"},
		{name: "no Source", hasSource: false,
			yaml: "services:\n  web:\n    image: nginx\n    volumes:\n      - ./src:/app\n"},
		{name: "long bind read_only false with the absolute src path", hasSource: true,
			wantExact: []string{`Service "web" mounts ` + dir + `/src writable at /data: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.`},
			yaml:      "services:\n  web:\n    image: nginx\n    volumes:\n      - {type: bind, source: " + dir + "/src, target: /data, read_only: false}\n"},
		{name: "services are reported in sorted order", hasSource: true,
			wantExact: []string{
				`Service "api" mounts ./src writable at /a: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.`,
				srcWarning,
			},
			yaml: "services:\n  web:\n    image: nginx\n    volumes: ['./src:/app']\n  api:\n    image: nginx\n    volumes: ['./src:/a']\n"},
		{name: "named volume with name: warns", wantExact: []string{volumeWarning},
			yaml: "services: {}\nvolumes:\n  data:\n    name: shared-db\n"},
		{name: "names another Compose that declares it", elsewhere: map[string]string{"shared-db": "api"},
			wantExact: []string{volumeWarning + ` Compose "api" also declares it.`},
			yaml:      "services: {}\nvolumes:\n  data:\n    name: shared-db\n"},
		{name: "external volume", elsewhere: map[string]string{"shared-db": "api"},
			yaml: "services: {}\nvolumes:\n  data:\n    name: shared-db\n    external: true\n"},
		{name: "old external syntax", yaml: "services: {}\nvolumes:\n  data:\n    external:\n      name: shared-db\n"},
		{name: "interpolated name", yaml: "services: {}\nvolumes:\n  data:\n    name: ${PREFIX}-data\n"},
		{name: "volume without name:", yaml: "services: {}\nvolumes:\n  data: {}\n  other:\n"},
		{name: "invalid YAML", yaml: "services: ["},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := volumeWarnings(c.yaml, dir, c.hasSource, c.elsewhere)
			if len(got) == 0 && len(c.wantExact) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.wantExact) {
				t.Fatalf("warnings = %q, want %q", got, c.wantExact)
			}
		})
	}
}
