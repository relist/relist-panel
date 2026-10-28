package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"relist-panel/internal/dockerhost"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/vulnscan"
)

type nopScanner struct{}

func (nopScanner) EnsureImage(context.Context, string) error { return nil }
func (nopScanner) UpdateDB(context.Context, string) (vulnscan.DBInfo, error) {
	return vulnscan.DBInfo{Version: "db"}, nil
}
func (nopScanner) DBInfo(context.Context, string) (vulnscan.DBInfo, error) {
	return vulnscan.DBInfo{Version: "db"}, nil
}
func (nopScanner) Scan(context.Context, string, string) (vulnscan.Report, error) {
	return vulnscan.Report{}, nil
}
func (nopScanner) ImageID(context.Context, string) (string, error) { return "sha256:scanner", nil }
func (nopScanner) RemoveImage(context.Context, string) error       { return nil }
func (nopScanner) DeleteCache(context.Context) error               { return nil }
func (nopScanner) CacheSize(context.Context) (int64, error)        { return 0, nil }
func (nopScanner) Cleanup(context.Context) error                   { return nil }

type noImages struct{}

func (noImages) Images(context.Context) ([]vulnscan.UsedImage, error) { return nil, nil }

func TestVulnerabilitiesRoutesWired(t *testing.T) {
	s := newTestServer(t)
	s.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	handler, wait := httpapi.NewHandler(ctx, httpapi.Deps{DB: s.db, Runner: s.runner, DataDir: s.dataDir,
		VulnScanner: nopScanner{}, VulnInUse: noImages{}})
	t.Cleanup(func() { cancel(); wait() })
	s.handler = handler
	response := s.request(t, http.MethodGet, "/api/vulnerabilities/settings", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", response.StatusCode)
	}
	response.Body.Close()
	setupAndLogin(t, s)
	response = s.request(t, http.MethodGet, "/api/vulnerabilities/settings", nil)
	if body := readBody(t, response); response.StatusCode != 200 || !strings.Contains(body, `"defaultScannerImage"`) {
		t.Fatalf("settings = %d %s", response.StatusCode, body)
	}
}

func TestVulnerabilitiesAbsentWithoutScanner(t *testing.T) {
	s := newTestServer(t)
	setupAndLogin(t, s)
	response := s.request(t, http.MethodGet, "/api/vulnerabilities/settings", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("settings without a scanner = %d", response.StatusCode)
	}
}

// scannerEngine offers the Scanner image and cache as prune candidates.
type scannerEngine struct{ hostEngine }

func (*scannerEngine) Candidates(_ context.Context, k dockerhost.Kind) ([]dockerhost.Candidate, error) {
	if k == dockerhost.UnusedImages {
		return []dockerhost.Candidate{{ID: "sha256:s", Name: "aquasec/trivy", Digests: []string{"aquasec/trivy@" + strings.SplitN(vulnscan.DefaultScannerImage, "@", 2)[1]}}}, nil
	}
	return []dockerhost.Candidate{{Name: vulnscan.CacheVolume}}, nil
}

func TestPruneKeepsScannerImageAndCache(t *testing.T) {
	s := newTestServer(t)
	s.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	handler, wait := httpapi.NewHandler(ctx, httpapi.Deps{DB: s.db, Runner: s.runner, DataDir: s.dataDir, Docker: &scannerEngine{},
		VulnScanner: nopScanner{}, VulnInUse: noImages{}})
	t.Cleanup(func() { cancel(); wait() })
	s.handler = handler
	setupAndLogin(t, s)
	for _, kind := range []string{"unused-images", "all-volumes"} {
		response := s.request(t, http.MethodGet, "/api/docker/prune-plan?kind="+kind, nil)
		if body := readBody(t, response); response.StatusCode != 200 || !strings.Contains(body, `"delete":[]`) || !strings.Contains(body, "Vulnerability scanner") {
			t.Fatalf("%s plan = %d %s", kind, response.StatusCode, body)
		}
	}
}
