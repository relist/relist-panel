package migration

import "relist-panel/internal/config"

// Options wires a server Migration into the panel's handler. A nil *Options
// leaves the Migration routes and the lock middleware out.
type Options struct {
	Docker          Docker
	Install         config.InstallSettings
	ReportedDataDir string // "" means the data directory
}
