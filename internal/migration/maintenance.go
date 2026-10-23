package migration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"relist-panel/internal/dockernet"
	"relist-panel/internal/domains"
)

const maintenanceService = "relist-maint"

// MaintenanceLabels are the Traefik labels of the maintenance container: one
// router per panel-managed Domain, with the Domain's own rule, entrypoint and
// certificate resolver, all served by the maintenance page on port 8080. An
// HTTPS Domain on the default entrypoint also gets a router on web, without
// the redirect, so plain HTTP visitors see the page too.
func MaintenanceLabels(ds []domains.Domain) map[string]string {
	labels := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": dockernet.NetworkName,
		"traefik.http.services." + maintenanceService + ".loadbalancer.server.port": "8080",
	}
	for _, d := range ds {
		router := fmt.Sprintf("traefik.http.routers.relist-maint-d%d", d.ID)
		labels[router+".rule"] = domains.Rule(d)
		labels[router+".entrypoints"] = domains.EntrypointFor(d)
		labels[router+".service"] = maintenanceService
		if !d.HTTPS {
			continue
		}
		labels[router+".tls"] = "true"
		switch d.CertProvider {
		case "letsencrypt":
			labels[router+".tls.certresolver"] = "letsencrypt"
		case "custom":
			labels[router+".tls.certresolver"] = d.CertResolver
		}
		if d.Entrypoint == "" {
			labels[router+"-http.rule"] = domains.Rule(d)
			labels[router+"-http.entrypoints"] = "web"
			labels[router+"-http.service"] = maintenanceService
		}
	}
	return labels
}

const maintenancePage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Down for maintenance</title>
<style>
:root { color-scheme: light dark; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; font-family: system-ui, sans-serif; background: Canvas; color: CanvasText; }
main { max-width: 32rem; padding: 2rem 1rem; text-align: center; }
h1 { font-size: 1.5rem; margin: 0 0 .5rem; }
p { margin: 0; opacity: .75; line-height: 1.5; }
</style>
</head>
<body>
<main>
<h1>Down for maintenance</h1>
<p>This site is moving to a new server and will be back shortly.</p>
</main>
</body>
</html>
`

func maintenanceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Retry-After", "300")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, maintenancePage)
	})
}

// ServeMaintenancePage is `relist-panel maintenance-page`: the whole program
// of the maintenance container, until ctx ends.
func ServeMaintenancePage(ctx context.Context, addr string) error {
	server := &http.Server{Addr: addr, Handler: maintenanceHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
