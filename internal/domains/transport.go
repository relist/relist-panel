package domains

import (
	"os"
	"path/filepath"
)

// TransportFileName is the file in Traefik's file-provider directory that
// defines the transport zero-downtime Domains name (ADR-0023). A Domain whose
// label names it is disabled while it is missing, so it is never deleted.
const TransportFileName = "relist-transports.yml"

// fastDialTransport fails a dial to a Container that has just gone within a
// second, so the retry middleware sends the request to the new one instead of
// waiting out Traefik's 30s default.
const fastDialTransport = `# Written by relist-panel; changes are overwritten.
http:
  serversTransports:
    relist-fastdial:
      forwardingTimeouts:
        dialTimeout: 1s
`

// WriteTransportFile writes the transport file into dir (Traefik's file-provider
// directory) through a temporary file and a rename, so Traefik's watcher never
// reads half a file.
func WriteTransportFile(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".transports-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(fastDialTransport); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, TransportFileName))
}
