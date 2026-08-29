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

	// NetworkName is the Docker network shared by the panel, Traefik, and
	// every deployed Compose, per ADR-0002. It is a fixed name, not
	// configurable, because docker-compose.yml (the install bootstrap)
	// references it by this exact literal.
	NetworkName = "relist-public"
)

// Config contains the small set of process-level settings needed by the
// walking skeleton. It intentionally has no dependency on a config library.
type Config struct {
	Port    int
	DataDir string
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

	return Config{Port: port, DataDir: dataDir}, nil
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
