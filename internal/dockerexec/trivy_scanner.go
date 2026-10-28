package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"relist-panel/internal/vulnscan"
)

const (
	scanTimeout     = 10 * time.Minute
	dbUpdateTimeout = 5 * time.Minute
	ensureTimeout   = 2 * time.Minute
	exportHeadroom  = 512 << 20
)

var imageIDPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// TrivyScanner implements vulnscan.Scanner. The panel exports the image over
// the Engine API to a tar in the data dir, and Trivy scans that one file in a
// container with no socket, no network and no capabilities (ADR-0021). The
// data dir is mounted at the same path on the host and in the panel, so the
// tar's path means the same thing to the daemon.
type TrivyScanner struct {
	client  *http.Client
	dataDir string
	// Bounds on one scan, one DB update, and one pull-and-check.
	scanTimeout, dbUpdateTimeout, ensureTimeout time.Duration
	run                                         func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
	free                                        func(path string) (uint64, error)
}

var _ vulnscan.Scanner = (*TrivyScanner)(nil)

func NewTrivyScanner(socketPath, dataDir string) *TrivyScanner {
	return &TrivyScanner{client: newSocketClient(socketPath), dataDir: dataDir, run: execDocker, free: freeBytes,
		scanTimeout: scanTimeout, dbUpdateTimeout: dbUpdateTimeout, ensureTimeout: ensureTimeout}
}

func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

func (s *TrivyScanner) workDir() string { return filepath.Join(s.dataDir, ".scan-work") }

// docker runs the CLI and returns its stdout, or an error carrying the line
// of its stderr that names the failure.
func (s *TrivyScanner) docker(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := s.run(ctx, append([]string{"docker"}, args...), nil, &stdout, &stderr); err != nil {
		return "", dockerError(err, stderr.String())
	}
	return stdout.String(), nil
}

func dockerError(err error, stderr string) error {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == 137 {
		return errors.New("scanner ran out of memory (1 GB limit)")
	}
	if line := errorLine(stderr); line != "" {
		return errors.New(line)
	}
	return err
}

// errorLine is Trivy's last FATAL or ERROR log line, without its timestamp:
// Trivy logs INFO lines first, so the first line rarely says what failed.
// Other CLI output keeps its first line, since Docker ends with usage hints.
func errorLine(stderr string) string {
	found := ""
	for _, line := range strings.Split(stderr, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) > 2 && (fields[1] == "FATAL" || fields[1] == "ERROR") {
			found = strings.Join(fields[2:], ": ")
		}
	}
	if found != "" {
		return found
	}
	return firstLine(stderr)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// EnsureImage pulls ref if it is missing, checks it runs locked down, and
// makes sure the cache volume exists and belongs to the Scanner's user.
func (s *TrivyScanner) EnsureImage(ctx context.Context, ref string) error {
	if !ValidScannerRef(ref) {
		return fmt.Errorf("invalid scanner image %q", ref)
	}
	ctx, cancel := context.WithTimeout(ctx, s.ensureTimeout)
	defer cancel()
	if _, err := s.docker(ctx, "image", "inspect", ref); err != nil {
		if _, err := s.docker(ctx, "pull", ref); err != nil {
			return fmt.Errorf("pull %s: %w", ref, err)
		}
	}
	if _, err := s.docker(ctx, append(lockedDown(""), "--network", "none", ref, "--version")...); err != nil {
		return fmt.Errorf("run %s: %w", ref, err)
	}
	// Only a volume created here needs its owner set, so root runs once per
	// volume rather than on every check.
	if _, err := s.docker(ctx, "volume", "inspect", trivyCacheVolume); err != nil {
		if _, err := s.docker(ctx, "volume", "create", trivyCacheVolume); err != nil {
			return fmt.Errorf("create %s: %w", trivyCacheVolume, err)
		}
		if _, err := s.docker(ctx, chownArgv(ref)...); err != nil {
			return fmt.Errorf("prepare %s: %w", trivyCacheVolume, err)
		}
	}
	return nil
}

func (s *TrivyScanner) UpdateDB(ctx context.Context, ref string) (vulnscan.DBInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, s.dbUpdateTimeout)
	defer cancel()
	if _, err := s.docker(ctx, dbUpdateArgv(ref)...); err != nil {
		if ctx.Err() != nil {
			s.removeContainer(dbUpdateName)
			return vulnscan.DBInfo{}, fmt.Errorf("update vulnerability DB: %w", ctx.Err())
		}
		return vulnscan.DBInfo{}, fmt.Errorf("update vulnerability DB: %w", err)
	}
	return s.DBInfo(ctx, ref)
}

// DBInfo asks Trivy which DB is in the cache, offline. No DB means no
// VulnerabilityDB in its answer.
func (s *TrivyScanner) DBInfo(ctx context.Context, ref string) (vulnscan.DBInfo, error) {
	out, err := s.docker(ctx, append(lockedDown(""), "--network", "none", ref,
		"--cache-dir", "/cache", "version", "-f", "json")...)
	if err != nil {
		return vulnscan.DBInfo{}, err
	}
	var v struct {
		VulnerabilityDB *struct {
			UpdatedAt string `json:"UpdatedAt"`
		} `json:"VulnerabilityDB"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return vulnscan.DBInfo{}, fmt.Errorf("read scanner version: %w", err)
	}
	if v.VulnerabilityDB == nil || v.VulnerabilityDB.UpdatedAt == "" {
		return vulnscan.DBInfo{}, vulnscan.ErrNoDB
	}
	return vulnscan.DBInfo{Version: v.VulnerabilityDB.UpdatedAt}, nil
}

func (s *TrivyScanner) Scan(ctx context.Context, ref, imageID string) (vulnscan.Report, error) {
	if !imageIDPattern.MatchString(imageID) {
		return vulnscan.Report{}, fmt.Errorf("invalid image id %q", imageID)
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, s.scanTimeout)
	defer cancel()
	var image struct {
		Size int64 `json:"Size"`
	}
	if err := s.engineJSON(ctx, "/images/"+imageID+"/json", &image); err != nil {
		return vulnscan.Report{}, err
	}
	if err := os.MkdirAll(s.workDir(), 0o755); err != nil {
		return vulnscan.Report{}, err
	}
	if free, err := s.free(s.workDir()); err != nil {
		return vulnscan.Report{}, err
	} else if free < uint64(max(image.Size, 0))+exportHeadroom {
		return vulnscan.Report{}, errors.New("not enough disk space for export")
	}
	hex := strings.TrimPrefix(imageID, "sha256:")
	tarPath := filepath.Join(s.workDir(), hex+".tar")
	defer os.Remove(tarPath)
	if err := s.export(ctx, imageID, tarPath); err != nil {
		return vulnscan.Report{}, err
	}

	pr, pw := io.Pipe()
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		err := s.run(ctx, append([]string{"docker"}, scanArgv(ref, hex, tarPath)...), nil, pw, &stderr)
		pw.CloseWithError(err)
		done <- err
	}()
	report, parseErr := ParseTrivy(pr)
	io.Copy(io.Discard, pr)
	if err := <-done; err != nil {
		if ctx.Err() != nil {
			// Killing the CLI leaves the container running: remove it too.
			s.removeContainer(scanName(hex))
			if parent.Err() != nil {
				return vulnscan.Report{}, parent.Err()
			}
			return vulnscan.Report{}, errors.New("scan timed out")
		}
		return vulnscan.Report{}, dockerError(err, stderr.String())
	}
	return report, parseErr
}

// export streams GET /images/{id}/get, the same tar as `docker image save`,
// to path, readable by the Scanner's user.
func (s *TrivyScanner) export(ctx context.Context, imageID, path string) error {
	resp, err := s.engine(ctx, "/images/"+imageID+"/get")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		return fmt.Errorf("export image: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func (s *TrivyScanner) engine(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, vulnscan.ErrImageGone
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("docker api %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (s *TrivyScanner) engineJSON(ctx context.Context, path string, out any) error {
	resp, err := s.engine(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	return nil
}

// removeContainer force-removes a Scanner container whose CLI was killed,
// with a context of its own, since the caller's has ended.
func (s *TrivyScanner) removeContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.docker(ctx, "rm", "-f", name); err != nil && !strings.Contains(err.Error(), "No such container") {
		log.Printf("vulnscan: remove %s: %v", name, err)
	}
}

func (s *TrivyScanner) ImageID(ctx context.Context, ref string) (string, error) {
	out, err := s.docker(ctx, "image", "inspect", "--format", "{{.Id}}", ref)
	return strings.TrimSpace(out), err
}

func (s *TrivyScanner) RemoveImage(ctx context.Context, ref string) error {
	if _, err := s.docker(ctx, "image", "rm", ref); err != nil && !strings.Contains(err.Error(), "No such image") {
		return err
	}
	return nil
}

func (s *TrivyScanner) DeleteCache(ctx context.Context) error {
	if _, err := s.docker(ctx, "volume", "rm", trivyCacheVolume); err != nil && !strings.Contains(err.Error(), "no such volume") {
		return err
	}
	return nil
}

// CacheSize reads the cache volume's size from GET /system/df, 0 if the
// volume does not exist.
func (s *TrivyScanner) CacheSize(ctx context.Context) (int64, error) {
	var df struct {
		Volumes []struct {
			Name      string `json:"Name"`
			UsageData struct {
				Size int64 `json:"Size"`
			} `json:"UsageData"`
		} `json:"Volumes"`
	}
	if err := s.engineJSON(ctx, "/system/df?type=volume", &df); err != nil {
		return 0, err
	}
	for _, v := range df.Volumes {
		if v.Name == trivyCacheVolume {
			return max(v.UsageData.Size, 0), nil
		}
	}
	return 0, nil
}

func (s *TrivyScanner) Cleanup(ctx context.Context) error {
	out, err := s.docker(ctx, "ps", "-aq", "--filter", "label="+trivyLabel)
	if err != nil {
		return err
	}
	if ids := strings.Fields(out); len(ids) > 0 {
		if _, err := s.docker(ctx, append([]string{"rm", "-f"}, ids...)...); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(s.workDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.workDir(), e.Name())); err != nil {
			return err
		}
	}
	return nil
}
