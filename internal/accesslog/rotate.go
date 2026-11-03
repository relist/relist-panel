package accesslog

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Traefik is the install's Traefik container, as the panel reaches it.
type Traefik interface {
	// Reopen sends USR1, so Traefik closes and reopens its Access log.
	Reopen(ctx context.Context) error
	Logs(ctx context.Context, tail int) (string, error)
	Args(ctx context.Context) ([]string, error)
}

var ErrNoTraefik = errors.New("no Traefik container in the install")

const dateLayout = "2006-01-02"

// pendingRetry is how soon Run comes back when a rotated file was too fresh
// to gzip.
const pendingRetry = 10 * time.Minute

// Rotator rotates the Access log once a day: it renames access.log after the
// day it ends on, has Traefik reopen it, gzips rotated files once Traefik has
// stopped writing them, and deletes files past the retention period.
type Rotator struct {
	Dir     string
	Store   *Store
	Traefik Traefik
	Now     func() time.Time
	// QuietFor is how long a rotated file must go unmodified before it is
	// gzipped: Traefik may still write to it if Reopen failed.
	QuietFor time.Duration
}

// Run rotates at once, then at every 00:00 UTC, until ctx is done.
func (r *Rotator) Run(ctx context.Context) {
	for {
		if err := r.RunOnce(ctx); err != nil {
			log.Printf("accesslog: %v", err)
		}
		pending, err := r.pendingGzip()
		if err != nil {
			log.Printf("accesslog: %v", err)
		}
		timer := time.NewTimer(nextWake(r.Now(), pending))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// nextWake is how long to sleep: until the next 00:00 UTC, or less when a
// rotated file still waits to be gzipped.
func nextWake(now time.Time, pending bool) time.Duration {
	now = now.UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	wait := midnight.Sub(now)
	if pending && pendingRetry < wait {
		return pendingRetry
	}
	return wait
}

// RunOnce does one rotation run and records its outcome. Every step runs even
// after an earlier one failed; the first error is returned.
func (r *Rotator) RunOnce(ctx context.Context) error {
	now := r.Now().UTC()
	var errs []error
	if err := r.rotate(ctx, now); err != nil {
		errs = append(errs, err)
	}
	if err := r.gzipQuiet(now); err != nil {
		errs = append(errs, err)
	}
	if err := r.expire(ctx, now); err != nil {
		errs = append(errs, err)
	}
	err := errors.Join(errs...)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if recErr := r.Store.RecordRun(ctx, now, msg); recErr != nil {
		log.Printf("accesslog: record run: %v", recErr)
	}
	return err
}

func today(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// rotate renames access.log when it holds lines from before today, then has
// Traefik reopen it.
func (r *Rotator) rotate(ctx context.Context, now time.Time) error {
	live := filepath.Join(r.Dir, liveName)
	if _, err := os.Lstat(live); errors.Is(err, os.ErrNotExist) {
		// Reopen creates access.log at once, so a missing one next to a
		// rotated file still being written means the last Reopen failed.
		writing, err := r.stillWritten(now)
		if err != nil || !writing {
			return err
		}
		if err := r.Traefik.Reopen(ctx); err != nil {
			return fmt.Errorf("have Traefik reopen the Access log: %w", err)
		}
		return nil
	}
	started, ok, err := firstLineTime(live)
	if err != nil || !ok || !started.Before(today(now)) {
		return err
	}
	name, err := r.freeName(today(now).AddDate(0, 0, -1).Format(dateLayout))
	if err != nil {
		return err
	}
	if err := os.Rename(live, filepath.Join(r.Dir, name)); err != nil {
		return fmt.Errorf("rotate: %w", err)
	}
	if err := r.Traefik.Reopen(ctx); err != nil {
		return fmt.Errorf("have Traefik reopen the Access log: %w", err)
	}
	return nil
}

// firstLineTime is when the file's first request started, from its StartUTC,
// or the file's mtime when that line does not parse. ok is false for a
// missing or empty file.
func firstLineTime(path string) (time.Time, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return time.Time{}, false, err
	}
	line, _ := bufio.NewReader(io.LimitReader(file, 64<<10)).ReadString('\n')
	var entry struct {
		StartUTC time.Time `json:"StartUTC"`
	}
	if json.Unmarshal([]byte(line), &entry) == nil && !entry.StartUTC.IsZero() {
		return entry.StartUTC, true, nil
	}
	return info.ModTime(), true, nil
}

// freeName is access-<date>.log, or access-<date>-N.log when that name or
// its gzipped form is taken.
func (r *Rotator) freeName(date string) (string, error) {
	for n := 1; ; n++ {
		base := "access-" + date
		if n > 1 {
			base += fmt.Sprintf("-%d", n)
		}
		taken := false
		for _, name := range []string{base + ".log", base + ".log.gz"} {
			_, err := os.Lstat(filepath.Join(r.Dir, name))
			if err == nil {
				taken = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
		if !taken {
			return base + ".log", nil
		}
	}
}

// rotatedFiles lists the rotated files in the directory, plain and gzipped.
func (r *Rotator) rotatedFiles() ([]os.DirEntry, error) {
	entries, err := os.ReadDir(r.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var out []os.DirEntry
	for _, e := range entries {
		if e.Type().IsRegular() && rotatedPattern.MatchString(e.Name()) {
			out = append(out, e)
		}
	}
	return out, err
}

// stillWritten reports whether a plain rotated file was modified within
// QuietFor, so Traefik may still hold it open.
func (r *Rotator) stillWritten(now time.Time) (bool, error) {
	files, err := r.rotatedFiles()
	for _, e := range files {
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		if info, statErr := e.Info(); statErr == nil && r.recent(info, now) {
			return true, err
		}
	}
	return false, err
}

func (r *Rotator) recent(info os.FileInfo, now time.Time) bool {
	return now.Sub(info.ModTime()) < r.QuietFor
}

func (r *Rotator) pendingGzip() (bool, error) {
	files, err := r.rotatedFiles()
	for _, e := range files {
		if strings.HasSuffix(e.Name(), ".log") {
			return true, err
		}
	}
	return false, err
}

// gzipQuiet gzips every plain rotated file not modified for QuietFor.
func (r *Rotator) gzipQuiet(now time.Time) error {
	files, err := r.rotatedFiles()
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range files {
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil || r.recent(info, now) {
			continue
		}
		if err := gzipFile(filepath.Join(r.Dir, e.Name())); err != nil {
			errs = append(errs, fmt.Errorf("gzip %s: %w", e.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// gzipFile writes path.gz through a temp file, so a crash never leaves a
// truncated .gz, and removes path only once the .gz is complete.
func gzipFile(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := path + ".gz.tmp"
	// A leftover temp, or a link planted under its name, is removed and the
	// temp created fresh with O_EXCL, so nothing is written through a link.
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(dst)
	_, err = io.Copy(zw, src)
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = dst.Sync()
	}
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path+".gz")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(path)
}

// expire deletes rotated files dated before today minus the retention days.
func (r *Rotator) expire(ctx context.Context, now time.Time) error {
	settings, err := r.Store.Get(ctx)
	if err != nil || settings.KeepForever {
		return err
	}
	cutoff := today(now).AddDate(0, 0, -settings.RetentionDays).Format(dateLayout)
	files, err := r.rotatedFiles()
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range files {
		date := rotatedPattern.FindStringSubmatch(e.Name())[1]
		if info, err := e.Info(); err == nil && r.recent(info, now) {
			// Traefik may still be writing it; deleting would lose every
			// later line into an unlinked file.
			continue
		}
		if date < cutoff {
			if err := os.Remove(filepath.Join(r.Dir, e.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
