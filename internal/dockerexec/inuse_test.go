package dockerexec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/vulnscan"
)

const (
	imgApp     = "sha256:" + "aa" + "00000000000000000000000000000000000000000000000000000000000000"
	imgPanel   = "sha256:" + "bb" + "00000000000000000000000000000000000000000000000000000000000000"
	imgTraefik = "sha256:" + "cc" + "00000000000000000000000000000000000000000000000000000000000000"
	imgOther   = "sha256:" + "dd" + "00000000000000000000000000000000000000000000000000000000000000"
)

func inUseEngine(t *testing.T) string {
	containers := `[
	 {"Id":"c1","ImageID":"` + imgApp + `","Labels":{"com.docker.compose.project":"shop-prod-web","com.docker.compose.service":"api"}},
	 {"Id":"c2","ImageID":"` + imgApp + `","Labels":{"com.docker.compose.project":"shop-prod-web","com.docker.compose.service":"worker"}},
	 {"Id":"panelcontainer","ImageID":"` + imgPanel + `","Labels":{"com.docker.compose.project":"relist","com.docker.compose.service":"panel"}},
	 {"Id":"t1","ImageID":"` + imgTraefik + `","Labels":{"com.docker.compose.project":"relist","com.docker.compose.service":"traefik"}},
	 {"Id":"x1","ImageID":"` + imgOther + `","Labels":{"com.docker.compose.project":"stranger","com.docker.compose.service":"db"}}
	]`
	return serveEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			io.WriteString(w, containers)
		case r.URL.Path == "/containers/panelcontainerhost/json":
			io.WriteString(w, `{"Id":"panelcontainer","Config":{"Labels":{"com.docker.compose.project":"relist"}}}`)
		case strings.HasPrefix(r.URL.Path, "/images/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/images/"), "/json")
			tags := map[string]string{imgApp: `["shop-web:latest"]`, imgPanel: `["relist-panel:latest"]`, imgTraefik: `["traefik:v3.7"]`}[id]
			if tags == "" {
				tags = "null"
			}
			io.WriteString(w, `{"Id":"`+id+`","RepoTags":`+tags+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func identities(context.Context) (map[string]vulnscan.ImageUser, error) {
	return map[string]vulnscan.ImageUser{"shop-prod-web": {Kind: vulnscan.UserService, ProjectID: 1, EnvironmentID: 2, ComposeID: 3}}, nil
}

func TestInUseClassifies(t *testing.T) {
	u := NewInUse(inUseEngine(t), identities, func() (string, error) { return "panelcontainerhost", nil })
	got, err := u.Images(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []vulnscan.UsedImage{
		{ImageID: imgApp, Refs: []string{"shop-web:latest"}, Users: []vulnscan.ImageUser{
			{Kind: vulnscan.UserService, ProjectID: 1, EnvironmentID: 2, ComposeID: 3, Service: "api"},
			{Kind: vulnscan.UserService, ProjectID: 1, EnvironmentID: 2, ComposeID: 3, Service: "worker"},
		}},
		{ImageID: imgPanel, Refs: []string{"relist-panel:latest"}, Users: []vulnscan.ImageUser{{Kind: vulnscan.UserPanel}}},
		{ImageID: imgTraefik, Refs: []string{"traefik:v3.7"}, Users: []vulnscan.ImageUser{{Kind: vulnscan.UserTraefik}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestInUseHostnameMissing(t *testing.T) {
	u := NewInUse(inUseEngine(t), identities, func() (string, error) { return "", errors.New("no hostname") })
	got, err := u.Images(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ImageID != imgApp {
		t.Fatalf("got %+v", got)
	}
}

func TestInUseHostnameNotAContainer(t *testing.T) {
	u := NewInUse(inUseEngine(t), identities, func() (string, error) { return "my-laptop", nil })
	got, err := u.Images(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// A redeploy can delete a container's image between the list and the
// inspect: that container is skipped, the rest of the list still answers.
func TestInUseSkipsAnImageThatVanished(t *testing.T) {
	engine := serveEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/json":
			io.WriteString(w, `[
			 {"Id":"c1","ImageID":"`+imgOther+`","Labels":{"com.docker.compose.project":"shop-prod-web","com.docker.compose.service":"old"}},
			 {"Id":"c2","ImageID":"`+imgApp+`","Labels":{"com.docker.compose.project":"shop-prod-web","com.docker.compose.service":"api"}}
			]`)
		case "/images/" + imgApp + "/json":
			io.WriteString(w, `{"Id":"`+imgApp+`","RepoTags":["shop-web:latest"]}`)
		default:
			http.Error(w, `{"message":"No such image"}`, http.StatusNotFound)
		}
	}))
	u := NewInUse(engine, identities, func() (string, error) { return "", nil })
	got, err := u.Images(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ImageID != imgApp {
		t.Fatalf("got %+v", got)
	}
}

func TestInUseImageInspectFailureIsAnError(t *testing.T) {
	engine := serveEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			io.WriteString(w, `[{"Id":"c1","ImageID":"`+imgApp+`","Labels":{"com.docker.compose.project":"shop-prod-web","com.docker.compose.service":"api"}}]`)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	u := NewInUse(engine, identities, func() (string, error) { return "", nil })
	if _, err := u.Images(context.Background()); err == nil {
		t.Fatal("want an error")
	}
}
