package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

const (
	defaultPort          = 8080
	defaultDataDir       = "/var/lib/relist-panel"
	defaultControlSocket = "/run/relist-panel/control.sock"
)

// Config contains the small set of process-level settings needed by the
// walking skeleton. It intentionally has no dependency on a config library.
type Config struct {
	Port    int
	DataDir string
	Install InstallSettings
	// AdminEmail and AdminPassword are RELIST_ADMIN_EMAIL and
	// RELIST_ADMIN_PASSWORD, the admin created at boot while none exists.
	AdminEmail    string
	AdminPassword string
	// ControlSocket is where the migration control socket listens
	// (ADR-0019), RELIST_CONTROL_SOCKET for a bare binary that cannot write
	// /run. InsecureOldHost is --insecure-migration-source, which lets the
	// new host pull from an http:// old host.
	ControlSocket   string
	InsecureOldHost bool
}

// InstallSettings are the install `.env` values a Migration cannot copy,
// because they live outside the data directory (ADR-0018). The install
// docker-compose.yml passes them to the panel; empty means unknown.
type InstallSettings struct {
	Domain    string `json:"domain"`
	ACMEEmail string `json:"acmeEmail"`
	HTTPPort  string `json:"httpPort"`
	HTTPSPort string `json:"httpsPort"`
}

func Load() (Config, error) {
	port, err := envPort()
	if err != nil {
		return Config{}, err
	}

	dataDir := os.Getenv("RELIST_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}

	install := InstallSettings{
		Domain:    os.Getenv("RELIST_DOMAIN"),
		ACMEEmail: os.Getenv("RELIST_ACME_EMAIL"),
		HTTPPort:  os.Getenv("TRAEFIK_HTTP_PORT"),
		HTTPSPort: os.Getenv("TRAEFIK_HTTPS_PORT"),
	}
	controlSocket := os.Getenv("RELIST_CONTROL_SOCKET")
	if controlSocket == "" {
		controlSocket = defaultControlSocket
	}
	return Config{Port: port, DataDir: dataDir, Install: install,
		AdminEmail: os.Getenv("RELIST_ADMIN_EMAIL"), AdminPassword: os.Getenv("RELIST_ADMIN_PASSWORD"),
		ControlSocket: controlSocket, InsecureOldHost: slices.Contains(os.Args[1:], "--insecure-migration-source")}, nil
}

func (c Config) Address() string {
	return ":" + strconv.Itoa(c.Port)
}

func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "panel.db")
}

func envPort() (int, error) {
	value := os.Getenv("RELIST_PORT")
	if value == "" {
		return defaultPort, nil
	}

	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("RELIST_PORT must be an integer between 1 and 65535, got %q", value)
	}
	return port, nil
}
