package requestmetrics

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	liveName     = "access.log"
	chunkSize    = 256 << 10
	missingRetry = 10 * time.Second
	defaultPoll  = time.Second
	// minuteKeep is how long minute rows are kept: the 24h window plus an
	// hour of slack, as metrics.RetentionHorizon.
	minuteKeep = 25 * time.Hour
)

// Ingester tails the Access log and turns it into metric rows (ADR-0035).
// Run drives it; tests call Step directly. It is not safe for concurrent use.
type Ingester struct {
	Dir   string // the Access log directory, holding access.log
	Store *Store
	Hosts HostLookup
	Now   func() time.Time
	Poll  time.Duration

	agg      *Aggregator
	f        *os.File
	inode    uint64
	bufStart int64 // file offset of buf[0]
	buf      []byte
	chunk    []byte

	sawMissing  bool
	missing     bool
	lastMaint   time.Time
	badLines    uint64
	loggedBad   uint64
	loggedLate  uint64
	loggedAhead uint64
	lastBadLog  time.Time
	lastErrLog  time.Time
}

func (in *Ingester) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// Run polls the Access log until ctx is done.
func (in *Ingester) Run(ctx context.Context) {
	for {
		if err := in.Step(ctx); err != nil && ctx.Err() == nil {
			in.logEvery("requestmetrics: %v", err)
		}
		wait := in.Poll
		if wait <= 0 {
			wait = defaultPoll
		}
		if in.missing {
			wait = missingRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (in *Ingester) logEvery(format string, args ...any) {
	now := in.now()
	if now.Sub(in.lastErrLog) < time.Minute {
		return
	}
	in.lastErrLog = now
	log.Printf(format, args...)
}

// Step is one poll: read what was appended, count it, close the minutes that
// are ready and write them with the cursor.
func (in *Ingester) Step(ctx context.Context) error {
	now := in.now()
	path := filepath.Join(in.Dir, liveName)

	if in.f == nil {
		if err := in.open(ctx, path, now); err != nil {
			return err
		}
		if in.f == nil {
			return nil
		}
	} else if err := in.followRotation(ctx, path, now); err != nil {
		return err
	}

	if fi, err := in.f.Stat(); err == nil && fi.Size() < in.bufStart+int64(len(in.buf)) {
		// Truncated in place: start over at the top of the same file.
		if _, err := in.f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		in.bufStart, in.buf = 0, in.buf[:0]
		in.agg.NewFile()
	}
	if err := in.drain(ctx, now); err != nil {
		return err
	}
	in.closeAndWrite(ctx, now)
	in.logCounters(now)
	return nil
}

func inodeOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

// open opens access.log for the first time, or after a switch left no open
// file, and decides where to start reading.
func (in *Ingester) open(ctx context.Context, path string, now time.Time) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		in.missing, in.sawMissing = true, true
		return nil
	}
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	in.missing = false
	in.f, in.inode = f, inodeOf(fi)
	in.buf, in.chunk = in.buf[:0], make([]byte, chunkSize)

	if in.agg != nil { // a switch to a new file while running
		in.bufStart = 0
		in.agg.NewFile()
		return nil
	}

	cur, ok, err := in.Store.Cursor(ctx)
	if err != nil {
		f.Close()
		in.f = nil
		return err
	}
	var offset int64
	var through time.Time
	switch {
	case ok && cur.Inode == in.inode && cur.ResumeOffset <= fi.Size():
		offset, through = cur.ResumeOffset, cur.FlushedThrough
	case in.sawMissing:
		// The file appeared after we started: all of it is new.
		through = time.Unix(0, 0)
		if ok {
			through = cur.FlushedThrough
		}
	default:
		// First start, or rotated while the panel was down: begin at the
		// end; the gap shows as missing buckets.
		offset, through = fi.Size(), now.Truncate(time.Minute)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		in.f = nil
		return err
	}
	in.bufStart = offset
	in.agg = NewAggregator(through, in.Hosts)
	return nil
}

// followRotation switches to a new access.log once the path points at a
// different file: the open file is read to EOF first, and the open
// descriptor keeps it readable even if the rotator removes it.
func (in *Ingester) followRotation(ctx context.Context, path string, now time.Time) error {
	fi, err := os.Stat(path)
	if err != nil || inodeOf(fi) == in.inode {
		return nil // no new file yet, or the same one
	}
	if err := in.drain(ctx, now); err != nil {
		return err
	}
	in.f.Close()
	in.f = nil
	// A fragment without a newline at the end of the old file is not a line.
	in.buf = in.buf[:0]
	return in.open(ctx, path, now)
}

// drain reads the open file to EOF.
func (in *Ingester) drain(ctx context.Context, now time.Time) error {
	for {
		n, err := in.f.Read(in.chunk)
		if n > 0 {
			in.consume(in.chunk[:n], now)
			in.closeAndWrite(ctx, now)
		}
		if err == io.EOF || (err == nil && n == 0) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// consume feeds the complete lines of data to the aggregator. A last line
// without its newline is held until the rest arrives.
func (in *Ingester) consume(data []byte, now time.Time) {
	in.buf = append(in.buf, data...)
	start := 0
	for {
		i := bytes.IndexByte(in.buf[start:], '\n')
		if i < 0 {
			break
		}
		raw := in.buf[start : start+i]
		offset := in.bufStart + int64(start)
		start += i + 1
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		l, err := ParseLine(raw)
		if err != nil {
			in.badLines++
			continue
		}
		in.agg.Add(l, offset, now)
	}
	if start > 0 {
		in.buf = append(in.buf[:0], in.buf[start:]...)
		in.bufStart += int64(start)
	}
}

func (in *Ingester) closeAndWrite(ctx context.Context, now time.Time) {
	rows := in.agg.CloseReady(now)
	if len(rows) == 0 {
		return
	}
	offset, through := in.agg.Cursor(in.bufStart)
	cur := Cursor{Inode: in.inode, ResumeOffset: offset, FlushedThrough: through}
	if err := in.Store.WriteMinutes(ctx, rows, cur); err != nil {
		// The minutes are dropped and show as a gap.
		in.logEvery("requestmetrics: write %d rows: %v", len(rows), err)
		return
	}
	in.maintain(ctx, through, now)
}

// maintain compacts the hours that are complete and deletes expired rows,
// once each time the written horizon crosses into a new hour.
func (in *Ingester) maintain(ctx context.Context, through, now time.Time) {
	hour := through.Truncate(time.Hour)
	if !hour.After(in.lastMaint) {
		return
	}
	first := hour.Add(-minuteKeep)
	if latest, ok, err := in.Store.LatestHour(ctx); err != nil {
		in.logEvery("requestmetrics: read latest hour: %v", err)
		return
	} else if ok && latest.Add(time.Hour).After(first) {
		first = latest.Add(time.Hour)
	}
	for h := first.Truncate(time.Hour); h.Before(hour); h = h.Add(time.Hour) {
		if err := in.Store.CompactHour(ctx, h); err != nil {
			in.logEvery("requestmetrics: compact %s: %v", h.Format(time.RFC3339), err)
			return
		}
	}
	days, err := in.Store.RetentionDays(ctx)
	if err != nil {
		in.logEvery("requestmetrics: read retention: %v", err)
		return
	}
	hourCutoff := now.Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Hour)
	if err := in.Store.DeleteBefore(ctx, now.Add(-minuteKeep).Truncate(time.Minute), hourCutoff); err != nil {
		in.logEvery("requestmetrics: delete expired rows: %v", err)
		return
	}
	in.lastMaint = hour
}

// logCounters reports skipped and dropped lines, at most once a minute.
func (in *Ingester) logCounters(now time.Time) {
	late, ahead := in.agg.Dropped(), in.agg.Future()
	if in.badLines == in.loggedBad && late == in.loggedLate && ahead == in.loggedAhead {
		return
	}
	if now.Sub(in.lastBadLog) < time.Minute {
		return
	}
	log.Printf("requestmetrics: skipped %d unreadable, dropped %d late and refused %d future-dated access log lines since the last report",
		in.badLines-in.loggedBad, late-in.loggedLate, ahead-in.loggedAhead)
	in.loggedBad, in.loggedLate, in.loggedAhead, in.lastBadLog = in.badLines, late, ahead, now
}
