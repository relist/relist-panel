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
	Exit            func()                          // nil means os.Exit(0)
}
