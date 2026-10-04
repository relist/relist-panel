package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/terminal"
)

// terminalServicesPath is the listing route under one Compose.
func terminalServicesPath(projectID, environmentID, composeID int64) string {
	return composePath(projectID, environmentID, composeID) + "/terminal/services"
}

type terminalServicesResponse struct {
	Available bool                      `json:"available"`
	Reason    string                    `json:"reason"`
	Services  []terminalServiceResponse `json:"services"`
}

type terminalServiceResponse struct {
	Name       string                      `json:"name"`
	Containers []terminalContainerResponse `json:"containers"`
}

type terminalContainerResponse struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Number int                    `json:"number"`
	Shells terminalShellsResponse `json:"shells"`
}

type terminalShellsResponse struct {
	Bash bool `json:"bash"`
	Sh   bool `json:"sh"`
}

// The listing reports every Service the Compose's status names, with its
// running Containers and which shells each one carries; a Service with no
// running Container is still listed, with an empty container list.
func TestTerminalServicesListsRunningContainersAndShells(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")
	projectName := "relist-c" + strconv.FormatInt(compose.ID, 10)

	server.runner.statuses[projectName] = composes.ComposeStatus{Services: []string{"app", "worker"}}
	server.terminal.Containers = map[string][]terminal.Container{
		projectName + "/app": {{ID: "c1", Name: "relist-c3-app-1", Number: 1}},
	}
	server.terminal.Shells = map[string]bool{
		"c1:/bin/bash": true,
		"c1:/bin/sh":   true,
	}

	response := server.request(t, http.MethodGet, terminalServicesPath(project.ID, env.ID, compose.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var payload terminalServicesResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode terminal services: %v", err)
	}
	if !payload.Available || payload.Reason != "" {
		t.Fatalf("availability = %+v, want available with no reason", payload)
	}
	if len(payload.Services) != 2 {
		t.Fatalf("services = %+v, want app and worker", payload.Services)
	}

	app := payload.Services[0]
	if app.Name != "app" || len(app.Containers) != 1 {
		t.Fatalf("first service = %+v, want app with one container", app)
	}
	container := app.Containers[0]
	if container.ID != "c1" || container.Name != "relist-c3-app-1" || container.Number != 1 {
		t.Fatalf("container = %+v, want c1/relist-c3-app-1/1", container)
	}
	if !container.Shells.Bash || !container.Shells.Sh {
		t.Fatalf("shells = %+v, want bash and sh", container.Shells)
	}

	worker := payload.Services[1]
	if worker.Name != "worker" {
		t.Fatalf("second service = %+v, want worker", worker)
	}
	if worker.Containers == nil || len(worker.Containers) != 0 {
		t.Fatalf("worker containers = %#v, want an empty list, not null", worker.Containers)
	}
}

// When the Engine API cannot be reached the listing is still a 200, but says
// the feature is unavailable and why — as the Volumes listing does.
func TestTerminalServicesReportsTheFeatureUnavailable(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")

	server.terminal.AvailableErr = errors.New("docker socket not reachable")

	response := server.request(t, http.MethodGet, terminalServicesPath(project.ID, env.ID, compose.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var payload terminalServicesResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode terminal services: %v", err)
	}
	if payload.Available {
		t.Fatalf("available = true, want false: %+v", payload)
	}
	if payload.Reason != "docker socket not reachable" {
		t.Fatalf("reason = %q, want the availability error", payload.Reason)
	}
	if payload.Services == nil || len(payload.Services) != 0 {
		t.Fatalf("services = %#v, want an empty list, not null", payload.Services)
	}
}

// An unknown Compose — or an unknown Environment or Project above it — is the
// same 404 the Compose routes answer, before any Engine API call.
func TestTerminalServicesRequiresAKnownComposeAndParents(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Terminal Co", "")
	env := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, env.ID, "App")

	for _, tt := range []struct {
		name string
		path string
	}{
		{"unknown compose", terminalServicesPath(project.ID, env.ID, compose.ID+1000)},
		{"unknown environment", terminalServicesPath(project.ID, env.ID+1000, compose.ID)},
		{"unknown project", terminalServicesPath(project.ID+1000, env.ID, compose.ID)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := server.request(t, http.MethodGet, tt.path, nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
			}
		})
	}
}

// The listing is behind the session cookie like every other API route.
func TestTerminalServicesRequiresAuthentication(t *testing.T) {
	server := newTestServer(t)
	response := server.request(t, http.MethodGet, terminalServicesPath(1, 1, 1), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}
