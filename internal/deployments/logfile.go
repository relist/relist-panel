package deployments

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
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

// ReadLog returns up to max bytes of the log from offset, cut back to the
// last whole rune so the text never ends in half a character, and the offset
// to continue from. A log shorter than offset reads as empty at offset.
func ReadLog(path string, offset int64, max int) (text string, next int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", offset, err
	}
	defer f.Close()
	buf := make([]byte, max)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", offset, err
	}
	buf = buf[:n]
	// A multi-byte rune may be cut by max, or still be half written.
	for back := 1; back < utf8.UTFMax && back <= len(buf); back++ {
		if utf8.RuneStart(buf[len(buf)-back]) {
			if !utf8.FullRune(buf[len(buf)-back:]) {
				buf = buf[:len(buf)-back]
			}
			break
		}
	}
	return string(buf), offset + int64(len(buf)), nil
}
