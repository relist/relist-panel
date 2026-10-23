package migration

import (
	"net/http"
	"time"

	"relist-panel/internal/config"
)

// Options wires a server Migration into the panel's handler. A nil *Options
// leaves the Migration routes and the lock middleware out.
type Options struct {
	Docker          Docker
	Install         config.InstallSettings
	Insecure        bool                            // --insecure-migration-source
	ReportedDataDir string                          // "" means the data directory
	HTTP            *http.Client                    // nil means a client with no overall timeout
	Backoff         func(attempt int) time.Duration // nil means 2s, 8s, 30s
	Restart         func()                          // nil means os.Exit(0); see FinalizeDeps.Restart
	// Control, when set, receives the routes the control socket serves
	// (ADR-0019); main serves it on Config.ControlSocket.
	Control *http.ServeMux
}
