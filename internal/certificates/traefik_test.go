package certificates

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestWriteDirWritesOneFilePerCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dynamic")
	certs := []Certificate{
		{ID: 3, CertificatePEM: "-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n", PrivateKeyPEM: "-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----\n"},
		{ID: 7, CertificatePEM: "cert-7", PrivateKeyPEM: "key-7"},
	}
	if err := WriteDir(dir, certs); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(readDir(t, dir), ","); got != "cert-3.yml,cert-7.yml" {
		t.Fatalf("files = %s", got)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", info.Mode().Perm())
	}
	for _, c := range certs {
		path := filepath.Join(dir, fileName(c.ID))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v", path, info.Mode().Perm())
		}
		raw, _ := os.ReadFile(path)
		if first, _, _ := strings.Cut(string(raw), "\n"); first != "# Written by relist-panel from the certificates table. Edits are overwritten." {
			t.Errorf("first line = %q", first)
		}
		var doc struct {
			TLS struct {
				Certificates []struct {
					CertFile string `yaml:"certFile"`
					KeyFile  string `yaml:"keyFile"`
				} `yaml:"certificates"`
			} `yaml:"tls"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.TLS.Certificates) != 1 || doc.TLS.Certificates[0].CertFile != c.CertificatePEM || doc.TLS.Certificates[0].KeyFile != c.PrivateKeyPEM {
			t.Errorf("%s = %+v", path, doc)
		}
	}
}

func TestWriteDirRemovesStaleCertFilesOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"cert-9.yml", "my-middlewares.yml", "cert-notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteDir(dir, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(readDir(t, dir), ","); got != "cert-notes.txt,my-middlewares.yml" {
		t.Errorf("files = %s", got)
	}
}

func TestWriteDirCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".traefik", "dynamic")
	if err := WriteDir(dir, []Certificate{{ID: 1, CertificatePEM: "c", PrivateKeyPEM: "k"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(readDir(t, dir), ","); got != "cert-1.yml" {
		t.Errorf("files = %s", got)
	}
}

func TestWriteDirLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	certs := []Certificate{{ID: 1, CertificatePEM: "c", PrivateKeyPEM: "k"}, {ID: 2, CertificatePEM: "c", PrivateKeyPEM: "k"}}
	for range 2 {
		if err := WriteDir(dir, certs); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(readDir(t, dir), ","); got != "cert-1.yml,cert-2.yml" {
		t.Errorf("files = %s", got)
	}
}

func TestWriteDirToleratesAlreadyDeletedFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDir(dir, []Certificate{{ID: 1, CertificatePEM: "c", PrivateKeyPEM: "k"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "cert-1.yml")); err != nil {
		t.Fatal(err)
	}
	if err := WriteDir(dir, nil); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
}

func TestDynamicDir(t *testing.T) {
	if got := DynamicDir("/var/lib/relist-panel"); got != "/var/lib/relist-panel/.traefik/dynamic" {
		t.Errorf("DynamicDir = %s", got)
	}
}
