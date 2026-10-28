package dockerexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"relist-panel/internal/vulnscan"
)

// The Scanner's fixed names and limits (ADR-0021).
const (
	trivyCacheVolume = vulnscan.CacheVolume
	trivyLabel       = "relist.vulnscan=1"
	trivyUser        = "65532:65532"
	trivyTmpfs       = "/tmp:rw,noexec,nosuid,size=512m,uid=65532"
)

// ValidScannerRef reports whether ref names an image by digest, the only
// form the panel pulls and runs as its Scanner.
func ValidScannerRef(ref string) bool {
	return vulnscan.ValidScannerRef(ref)
}

type trivyOutput struct {
	Metadata struct {
		OS *struct {
			Family string `json:"Family"`
			Name   string `json:"Name"`
		} `json:"OS"`
	} `json:"Metadata"`
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			PkgPath          string `json:"PkgPath"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
			PrimaryURL       string `json:"PrimaryURL"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// ParseTrivy translates Trivy's JSON report into findings, unfiltered:
// what to drop is vulnscan's decision.
func ParseTrivy(r io.Reader) (vulnscan.Report, error) {
	var out trivyOutput
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return vulnscan.Report{}, errors.New("scanner produced no output")
		}
		return vulnscan.Report{}, fmt.Errorf("read scanner output: %w", err)
	}
	var report vulnscan.Report
	if os := out.Metadata.OS; os != nil {
		report.OS = strings.TrimSpace(os.Family + " " + os.Name)
	}
	for _, result := range out.Results {
		for _, v := range result.Vulnerabilities {
			report.Findings = append(report.Findings, vulnscan.Finding{
				VulnID:           v.VulnerabilityID,
				PkgName:          v.PkgName,
				PkgPath:          v.PkgPath,
				InstalledVersion: v.InstalledVersion,
				FixedVersion:     v.FixedVersion,
				Severity:         vulnscan.Severity(v.Severity),
				Title:            v.Title,
				URL:              v.PrimaryURL,
			})
		}
	}
	return report, nil
}

// lockedDown is the part of every Scanner run that keeps it powerless: no
// capabilities, a read-only root, a non-root user, bounded CPU and memory,
// and only the cache volume mounted. It never mounts the Docker socket.
// An empty name lets Docker pick one, for short checks that may overlap.
func lockedDown(name string) []string {
	argv := []string{"run", "--rm"}
	if name != "" {
		argv = append(argv, "--name", name)
	}
	return append(argv, "--label", trivyLabel,
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--user", trivyUser, "--cpus", "1", "--memory", "1g",
		"--tmpfs", trivyTmpfs, "-v", trivyCacheVolume+":/cache")
}

const dbUpdateName = "relist-vulnscan-db"

func scanName(imageHex string) string {
	return "relist-vulnscan-" + imageHex[:min(12, len(imageHex))]
}

// scanArgv scans the exported image at tarPath with no network at all.
func scanArgv(ref, imageHex, tarPath string) []string {
	argv := lockedDown(scanName(imageHex))
	argv = append(argv, "--network", "none", "-v", tarPath+":/work/image.tar:ro", ref,
		"--cache-dir", "/cache", "image", "--input", "/work/image.tar", "--scanners", "vuln",
		"--skip-db-update", "--skip-java-db-update", "--offline-scan", "-f", "json", "-q")
	return argv
}

// dbUpdateArgv downloads the vulnerability DB. It is the one run with network,
// and it holds no image.
func dbUpdateArgv(ref string) []string {
	return append(lockedDown(dbUpdateName), ref,
		"--cache-dir", "/cache", "image", "--download-db-only", "--no-progress")
}

// chownArgv gives the cache volume to the Scanner's user. It runs as root
// with CHOWN as its only capability, and nothing else to reach.
func chownArgv(ref string) []string {
	return []string{"run", "--rm", "--label", trivyLabel, "--user", "0", "--read-only",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--security-opt", "no-new-privileges", "--network", "none",
		"--memory", "64m", "--pids-limit", "16",
		"-v", trivyCacheVolume + ":/v", "--entrypoint", "chown", ref, trivyUser, "/v"}
}
