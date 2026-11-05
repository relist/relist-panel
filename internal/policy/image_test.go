package policy

import "testing"

func TestImageLabels(t *testing.T) {
	router := map[string]string{"traefik.http.routers.x.rule": "HostRegexp(`.+`)", "traefik.http.routers.x.priority": "200000"}
	with := func(base map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name      string
		image     map[string]string
		container map[string]string
		want      int
	}{
		{"the image turns Traefik on", with(router, map[string]string{"traefik.enable": "true"}), nil, 3},
		{"the Compose turns Traefik on", router, map[string]string{"traefik.enable": "true"}, 2},
		{"Traefik is off", with(router, map[string]string{"traefik.enable": "true"}), map[string]string{"traefik.enable": "false"}, 0},
		{"Traefik is never turned on", router, nil, 0},
		{"another spelling still turns it on", with(router, map[string]string{"Traefik.Enable": "true"}), map[string]string{"traefik.enable": "false"}, 3},
		{"a key the Compose overrides", map[string]string{"traefik.http.routers.x.rule": "HostRegexp(`.+`)"}, map[string]string{"traefik.enable": "true", "traefik.http.routers.x.rule": "Host(`a.example.com`)"}, 0},
		{"labels that are not Traefik's", map[string]string{"org.opencontainers.image.title": "app"}, map[string]string{"traefik.enable": "true"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ImageLabels("web", "app:1", c.image, c.container)
			if len(got) != c.want {
				t.Fatalf("violations = %+v, want %d", got, c.want)
			}
			for _, v := range got {
				if v.Service != "web" || v.Message == "" {
					t.Errorf("violation %+v", v)
				}
			}
		})
	}
}
