package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	defaultPort    = 8080
	defaultDataDir = "/var/lib/relist-panel"
)

// Config contains the small set of process-level settings needed by the
// walking skeleton. It intentionally has no dependency on a config library.
type Config struct {
	Port    int
	DataDir string
	Install InstallSettings
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
	return Config{Port: port, DataDir: dataDir, Install: install}, nil
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
