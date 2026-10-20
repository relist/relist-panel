package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestServeControlFailsWithoutStoppingThePanel(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := serveControl(filepath.Join(blocker, "run", "control.sock"), http.NewServeMux())
	if err == nil {
		t.Fatal("serveControl under a regular file succeeded")
	}
	if srv != nil {
		t.Fatal("a server was returned with the error")
	}
}
