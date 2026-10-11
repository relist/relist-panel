package deployments

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	logDirName = ".deployments"
	logDirPerm = 0o700
	logPerm    = 0o600
)

// LogPath is where a Deployment's log lives inside the Compose directory.
func LogPath(composeDir string, number int) string {
	return filepath.Join(composeDir, logDirName, fmt.Sprintf("%d.log", number))
}

// openLog opens the Deployment's log for appending, creating .deployments/.
func openLog(composeDir string, number int) (*os.File, error) {
	if err := os.MkdirAll(filepath.Join(composeDir, logDirName), logDirPerm); err != nil {
		return nil, err
	}
	return os.OpenFile(LogPath(composeDir, number), os.O_APPEND|os.O_CREATE|os.O_WRONLY, logPerm)
}

// removeLogs deletes the logs of pruned Deployments; a missing file is fine.
func removeLogs(composeDir string, numbers []int) {
	for _, n := range numbers {
		if err := os.Remove(LogPath(composeDir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			logf("remove deployment log %d: %v", n, err)
		}
	}
}
