package dockerexec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testScannerRef = "aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c"

func TestParseTrivyFixtures(t *testing.T) {
	tests := []struct {
		file     string
		os       string
		findings int
		check    func(t *testing.T, got []string)
	}{
		{"os-alpine.json", "alpine 3.22.1", 2, nil},
		{"go-binary.json", "alpine 3.22.1", 3, nil},
		{"node.json", "alpine 3.21.4", 2, nil},
		{"debian-libc.json", "debian 12.11", 3, nil},
		{"clean.json", "", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			file, err := os.Open(filepath.Join("testdata", "trivy", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			r, err := ParseTrivy(file)
			if err != nil {
				t.Fatalf("ParseTrivy: %v", err)
			}
			if r.OS != tt.os || len(r.Findings) != tt.findings {
				t.Fatalf("OS %q, %d findings: %+v", r.OS, len(r.Findings), r.Findings)
			}
		})
	}
}

func TestParseTrivyFields(t *testing.T) {
	file, _ := os.Open(filepath.Join("testdata", "trivy", "go-binary.json"))
	defer file.Close()
	r, err := ParseTrivy(file)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Findings[0]
	if got.VulnID != "CVE-2026-2001" || got.PkgName != "golang.org/x/net" || got.PkgPath != "usr/local/bin/relist-panel" ||
		got.InstalledVersion != "v0.40.0" || got.FixedVersion != "0.41.0" || got.Severity != "HIGH" ||
		got.Title != "x/net: html parse" || got.URL != "https://avd.aquasec.com/nvd/cve-2026-2001" {
		t.Fatalf("finding = %+v", got)
	}
	file2, _ := os.Open(filepath.Join("testdata", "trivy", "debian-libc.json"))
	defer file2.Close()
	r, _ = ParseTrivy(file2)
	if r.Findings[0].PkgName != "linux-libc-dev" {
		t.Fatalf("parser must not filter: %+v", r.Findings[0])
	}
}

func TestParseTrivyMalformed(t *testing.T) {
	if _, err := ParseTrivy(strings.NewReader(`{"Results": [`)); err == nil {
		t.Fatal("want error")
	}
	if _, err := ParseTrivy(strings.NewReader(``)); err == nil {
		t.Fatal("want error for empty output")
	}
}

func TestValidRef(t *testing.T) {
	hex := strings.Repeat("ab", 32)
	for _, ref := range []string{
		"aquasec/trivy@sha256:" + hex,
		"ghcr.io/aquasecurity/trivy:0.76.1@sha256:" + hex,
		"localhost:5000/trivy:0.76.1@sha256:" + hex,
		testScannerRef,
	} {
		if !ValidScannerRef(ref) {
			t.Errorf("rejected %q", ref)
		}
	}
	for _, ref := range []string{
		"aquasec/trivy:0.76.1",
		"aquasec/trivy:latest",
		"aquasec/trivy@sha256:" + hex[:63],
		"aquasec/trivy@sha256:" + strings.ToUpper(hex),
		"aquasec/trivy@sha256:" + hex + " --privileged",
		"-v /:/host@sha256:" + hex,
		"",
	} {
		if ValidScannerRef(ref) {
			t.Errorf("accepted %q", ref)
		}
	}
}

// pair reports whether flag is directly followed by value.
func pair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

func assertNoEscape(t *testing.T, argv []string) {
	t.Helper()
	for _, a := range argv {
		if strings.Contains(a, "docker.sock") || a == "--privileged" || strings.HasPrefix(a, "--cap-add") && a != "--cap-add" {
			t.Fatalf("argv escapes the lockdown: %q in %v", a, argv)
		}
	}
	if slices.Contains(argv, "--network") && !pair(argv, "--network", "none") {
		t.Fatalf("network not none: %v", argv)
	}
}

func assertLockdown(t *testing.T, argv []string) {
	t.Helper()
	for _, p := range [][2]string{
		{"--cap-drop", "ALL"},
		{"--security-opt", "no-new-privileges"},
		{"--user", "65532:65532"},
		{"--cpus", "1"},
		{"--memory", "1g"},
		{"--tmpfs", "/tmp:rw,noexec,nosuid,size=512m,uid=65532"},
		{"-v", "relist-trivy-cache:/cache"},
		{"--label", "relist.vulnscan=1"},
	} {
		if !pair(argv, p[0], p[1]) {
			t.Errorf("missing %s %s in %v", p[0], p[1], argv)
		}
	}
	if !slices.Contains(argv, "--read-only") || !slices.Contains(argv, "--rm") {
		t.Errorf("missing --read-only/--rm in %v", argv)
	}
	if slices.Contains(argv, "--cap-add") {
		t.Errorf("scanner gains a capability: %v", argv)
	}
	assertNoEscape(t, argv)
}

func TestScanArgvLockdown(t *testing.T) {
	hex := strings.Repeat("c", 64)
	argv := scanArgv(testScannerRef, hex, "/var/lib/relist-panel/.scan-work/"+hex+".tar")
	if argv[0] != "run" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
	assertLockdown(t, argv)
	for _, p := range [][2]string{
		{"--network", "none"},
		{"--name", "relist-vulnscan-" + hex[:12]},
		{"-v", "/var/lib/relist-panel/.scan-work/" + hex + ".tar:/work/image.tar:ro"},
		{"--cache-dir", "/cache"},
		{"--input", "/work/image.tar"},
		{"--scanners", "vuln"},
		{"-f", "json"},
		// Java archives are skipped: scanning them needs the Java DB.
		{"--skip-files", "**/*.jar"},
		{"--skip-files", "**/*.war"},
		{"--skip-files", "**/*.ear"},
		{"--skip-files", "**/*.par"},
	} {
		if !pair(argv, p[0], p[1]) {
			t.Errorf("missing %s %s in %v", p[0], p[1], argv)
		}
	}
	for _, flag := range []string{"--skip-db-update", "--skip-java-db-update", "--offline-scan", "-q", "image"} {
		if !slices.Contains(argv, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	// The image reference comes before trivy's own arguments and after every docker flag.
	i := slices.Index(argv, testScannerRef)
	if i < 0 || argv[i+1] != "--cache-dir" {
		t.Fatalf("image position wrong: %v", argv)
	}
}

func TestDBUpdateArgv(t *testing.T) {
	argv := dbUpdateArgv(testScannerRef)
	assertLockdown(t, argv)
	if slices.Contains(argv, "--network") {
		t.Fatalf("DB update needs the network: %v", argv)
	}
	for _, a := range argv {
		if strings.Contains(a, "/work") {
			t.Fatalf("DB update mounts a tar: %v", argv)
		}
	}
	if !pair(argv, "--name", "relist-vulnscan-db") || !slices.Contains(argv, "--download-db-only") {
		t.Fatalf("argv = %v", argv)
	}
}

func TestChownArgv(t *testing.T) {
	argv := chownArgv(testScannerRef)
	for _, p := range [][2]string{
		{"--user", "0"},
		{"--cap-drop", "ALL"},
		{"--cap-add", "CHOWN"},
		{"--network", "none"},
		{"--security-opt", "no-new-privileges"},
		{"--entrypoint", "chown"},
		{"-v", "relist-trivy-cache:/v"},
		{"--label", "relist.vulnscan=1"},
	} {
		if !pair(argv, p[0], p[1]) {
			t.Errorf("missing %s %s in %v", p[0], p[1], argv)
		}
	}
	if !slices.Contains(argv, "--read-only") {
		t.Errorf("missing --read-only")
	}
	if argv[len(argv)-2] != "65532:65532" || argv[len(argv)-1] != "/v" {
		t.Fatalf("chown target = %v", argv[len(argv)-2:])
	}
	assertNoEscape(t, argv)
}
