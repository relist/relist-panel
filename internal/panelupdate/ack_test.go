package panelupdate

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"testing"
)

func TestIsTerminalAborted(t *testing.T) {
	if !IsTerminal(StateAborted) {
		t.Error("aborted should be terminal")
	}
	if IsTerminal(StateQuiescing) {
		t.Error("quiescing should not be terminal")
	}
}

func TestAckRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := PanelAck{Run: "20261101T100000Z", State: AckWaiting, WaitingOn: "Deployment api #3", Heartbeat: 4}
	if err := WriteAck(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAck(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	info, err := os.Stat(dir + "/panel.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestReadAckMissing(t *testing.T) {
	_, err := ReadAck(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func TestWriteAckIsAtomic(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := WriteAck(dir, PanelAck{Run: "r", State: AckReady, Heartbeat: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "panel.json" {
		t.Errorf("dir holds %v, want only panel.json", entries)
	}
}
