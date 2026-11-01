package panelupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const ackFile = "panel.json"

// Ack states: what the panel tells the Updater about a quiesce request.
const (
	AckWaiting = "waiting"
	AckReady   = "ready"
	AckRefused = "refused"
)

// PanelAck is panel.json, the panel's answer to a quiesce request. The panel is
// its only writer. Heartbeat is a counter the panel raises on every write, not
// a time, so the Updater never compares two Containers' clocks.
type PanelAck struct {
	Run       string `json:"run"`
	State     string `json:"state"`
	WaitingOn string `json:"waitingOn,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Heartbeat int64  `json:"heartbeat"`
}

// WriteAck replaces dir/panel.json atomically: a synced temp file in the same
// directory, then a rename.
func WriteAck(dir string, a PanelAck) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ack-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmp.Name(), filepath.Join(dir, ackFile))
}

// ReadAck reads dir/panel.json. A missing file is fs.ErrNotExist.
func ReadAck(dir string) (PanelAck, error) {
	data, err := os.ReadFile(filepath.Join(dir, ackFile))
	if err != nil {
		return PanelAck{}, err
	}
	var a PanelAck
	if err := json.Unmarshal(data, &a); err != nil {
		return PanelAck{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, ackFile), err)
	}
	return a, nil
}
