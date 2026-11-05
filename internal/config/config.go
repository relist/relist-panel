package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"relist-panel/internal/policy"
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
	// ControlSocket is where the migration control socket listens
	// (ADR-0019), RELIST_CONTROL_SOCKET for a bare binary that cannot write
	// /run. InsecureOldHost is --insecure-migration-source, which lets the
	// new host pull from an http:// old host.
	ControlSocket   string
	InsecureOldHost bool
	// Policy is the Compose policy's environment-only settings (ADR-0032).
	Policy Policy
}

// Policy is who approves a violating Compose and which host paths outside a
// Compose's directory it may bind without approval.
type Policy struct {
	Approver policy.Approver
	Roots    []string
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
	approver, err := policy.ParseApprover(os.Getenv("RELIST_POLICY_APPROVER"))
	if err != nil {
		return Config{}, err
	}
	roots, err := policy.ParseRoots(os.Getenv("RELIST_POLICY_BIND_ROOTS"))
	if err != nil {
		return Config{}, err
	}
	controlSocket := os.Getenv("RELIST_CONTROL_SOCKET")
	if controlSocket == "" {
		controlSocket = defaultControlSocket
	}
	return Config{Port: port, DataDir: dataDir, Install: install,
		ControlSocket: controlSocket, InsecureOldHost: slices.Contains(os.Args[1:], "--insecure-migration-source"),
		Policy: Policy{Approver: approver, Roots: roots}}, nil
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
