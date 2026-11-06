package requestmetrics

import (
	"sort"
	"time"
)

// Series scopes.
const (
	ScopeTotal = "total"
	ScopePanel = "panel"
	ScopeHost  = "host"
)

const (
	// closeGrace is how far past a minute's end the watermark must reach
	// before the minute closes: it absorbs lines written slightly out of
	// order.
	closeGrace = 30 * time.Second
	// idleClose closes minutes by the wall clock once no line has arrived
	// for this long, so a quiet site still gets its rows.
	idleClose = 2 * time.Minute
	// maxAhead is how far past the wall clock a line's StartUTC may be. A
	// line further ahead (a clock that jumped) would push the watermark
	// there, close every minute up to it and drop all real traffic after.
	maxAhead = 5 * time.Minute
	// fillKeep bounds the zero rows written for quiet minutes to what the
	// minute table keeps.
	fillKeep = 25 * time.Hour
)

// Key identifies one series. ComposeID and Host are zero values unless Scope
// is ScopeHost.
type Key struct {
	Scope     string
	ComposeID int64
	Host      string
}

func (k Key) less(o Key) bool {
	if k.Scope != o.Scope {
		return k.Scope < o.Scope
	}
	if k.ComposeID != o.ComposeID {
		return k.ComposeID < o.ComposeID
	}
	return k.Host < o.Host
}

// Counts is one series in one bucket: requests by status class plus the
// latency histogram.
type Counts struct {
	C1xx, C2xx, C3xx, C4xx, C5xx, Cother uint64
	Latency                              Histogram
}

// Add counts one request. A status outside 100..599 counts as other.
func (c *Counts) Add(status int, d time.Duration) {
	switch {
	case status >= 100 && status < 200:
		c.C1xx++
	case status >= 200 && status < 300:
		c.C2xx++
	case status >= 300 && status < 400:
		c.C3xx++
	case status >= 400 && status < 500:
		c.C4xx++
	case status >= 500 && status < 600:
		c.C5xx++
	default:
		c.Cother++
	}
	c.Latency.Observe(d)
}

// Merge adds o to c.
func (c *Counts) Merge(o *Counts) {
	c.C1xx += o.C1xx
	c.C2xx += o.C2xx
	c.C3xx += o.C3xx
	c.C4xx += o.C4xx
	c.C5xx += o.C5xx
	c.Cother += o.Cother
	c.Latency.Merge(&o.Latency)
}

// Requests is the number of requests counted.
func (c Counts) Requests() uint64 {
	return c.C1xx + c.C2xx + c.C3xx + c.C4xx + c.C5xx + c.Cother
}

// Row is one closed bucket of one series.
type Row struct {
	Key      Key
	BucketAt time.Time
	Counts   Counts
}

// HostLookup turns a Domain router's Compose and Domain IDs into the
// Domain's host, as stored.
type HostLookup func(composeID, domainID int64) (host string, ok bool)

type openMinute struct {
	series    map[Key]*Counts
	minOffset int64 // smallest offset among lines of generation gen
	gen       int
}

// Aggregator groups lines into per-minute buckets by their own StartUTC and
// closes a minute once the watermark (latest StartUTC read) has passed it by
// a grace, or once the log has been idle. It is not safe for concurrent use.
type Aggregator struct {
	hosts       HostLookup
	open        map[int64]*openMinute // unix seconds of the minute start
	watermark   time.Time
	lastAddAt   time.Time // last line counted, or the first CloseReady with none yet
	closedUntil int64     // unix seconds: every minute before this is closed
	floor       int64     // unix seconds: minutes before this were written by an earlier run
	gen         int
	dropped     uint64
	future      uint64
}

// NewAggregator starts an aggregator that treats every minute before
// flushedThrough as already written.
func NewAggregator(flushedThrough time.Time, hosts HostLookup) *Aggregator {
	return &Aggregator{
		hosts:       hosts,
		open:        map[int64]*openMinute{},
		closedUntil: flushedThrough.Unix(),
		floor:       flushedThrough.Unix(),
	}
}

// Add counts a line read at offset. now is the wall clock, used for idle
// closing. It returns false when the line was not counted: its minute was
// written before a restart (skipped), or closed since (dropped and counted).
func (a *Aggregator) Add(l Line, offset int64, now time.Time) bool {
	if l.Start.After(now.Add(maxAhead)) {
		a.future++
		return false
	}
	minute := l.Start.Unix() - l.Start.Unix()%60
	if minute < a.floor {
		return false // already written before a restart; not a late line
	}
	if minute < a.closedUntil {
		a.dropped++
		return false
	}
	a.lastAddAt = now
	if l.Start.After(a.watermark) {
		a.watermark = l.Start
	}
	m := a.open[minute]
	if m == nil {
		m = &openMinute{series: map[Key]*Counts{}, minOffset: offset, gen: a.gen}
		a.open[minute] = m
	} else if m.gen != a.gen {
		m.gen, m.minOffset = a.gen, offset
	} else if offset < m.minOffset {
		m.minOffset = offset
	}

	a.count(m, Key{Scope: ScopeTotal}, l)
	route := ClassifyRouter(l.Router)
	switch route.Kind {
	case RoutePanel:
		a.count(m, Key{Scope: ScopePanel}, l)
	case RouteDomain:
		if host, ok := a.hosts(route.ComposeID, route.DomainID); ok {
			a.count(m, Key{Scope: ScopeHost, ComposeID: route.ComposeID, Host: host}, l)
		}
	}
	return true
}

func (a *Aggregator) count(m *openMinute, k Key, l Line) {
	c := m.series[k]
	if c == nil {
		c = &Counts{}
		m.series[k] = c
	}
	c.Add(l.Status, l.Duration)
}

// CloseReady closes every minute that is ready at now, oldest first, and
// returns its rows sorted by minute, then key. A minute is ready once the
// watermark is 30 seconds past its end, or, after 2 minutes without a line,
// once the clock is. Closing walks minute by minute from where the last call
// stopped, so it always closes a prefix, and every closed minute gets a total
// row: zero for a minute without requests. A total row therefore means "the
// panel was collecting this minute"; no row means it was not.
func (a *Aggregator) CloseReady(now time.Time) []Row {
	if a.lastAddAt.IsZero() {
		a.lastAddAt = now // idle is measured from the first look at the log
	}
	idle := now.Sub(a.lastAddAt) >= idleClose
	ready := func(m int64) bool {
		end := time.Unix(m, 0).Add(time.Minute + closeGrace)
		return !a.watermark.Before(end) || (idle && !now.Before(end))
	}

	fillFrom := now.Add(-fillKeep).Truncate(time.Minute).Unix()
	var rows []Row
	m := a.closedUntil
	for {
		if m < fillFrom {
			// Far behind (no history, or a long outage): close the old open
			// minutes that remain, but write no zero rows that old.
			next := fillFrom
			for om := range a.open {
				if om >= m && om < next {
					next = om
				}
			}
			m = next
		}
		if !ready(m) {
			break
		}
		start := time.Unix(m, 0).UTC()
		series := map[Key]*Counts{}
		if om := a.open[m]; om != nil {
			series = om.series
			delete(a.open, m)
		}
		if series[Key{Scope: ScopeTotal}] == nil {
			series[Key{Scope: ScopeTotal}] = &Counts{}
		}
		keys := make([]Key, 0, len(series))
		for k := range series {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
		for _, k := range keys {
			rows = append(rows, Row{Key: k, BucketAt: start, Counts: *series[k]})
		}
		m += 60
	}
	if m > a.closedUntil {
		a.closedUntil = m
	}
	return rows
}

// NewFile marks a switch to a new access log file: offsets recorded before
// it no longer say where to resume.
func (a *Aggregator) NewFile() { a.gen++ }

// Cursor says where a restart resumes: the smallest offset of a line held in
// a still-open minute (readOffset when none is), and the start of the oldest
// open minute (the end of the newest closed one when none is open). Every
// line before the offset, and every minute before the time, is in a written
// row.
func (a *Aggregator) Cursor(readOffset int64) (int64, time.Time) {
	resume := readOffset
	first := true
	oldest := a.closedUntil
	oldestSet := false
	for minute, m := range a.open {
		if !oldestSet || minute < oldest {
			oldest, oldestSet = minute, true
		}
		if m.gen != a.gen {
			continue
		}
		if first || m.minOffset < resume {
			resume, first = m.minOffset, false
		}
	}
	return resume, time.Unix(oldest, 0).UTC()
}

// Future is the number of lines refused because their StartUTC was more
// than 5 minutes ahead of the clock.
func (a *Aggregator) Future() uint64 { return a.future }

// Dropped is the number of lines dropped because their minute had closed.
func (a *Aggregator) Dropped() uint64 { return a.dropped }
