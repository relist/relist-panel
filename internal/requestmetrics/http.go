package requestmetrics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/domains"
	"relist-panel/internal/traefikconfig"
)

// window is one chart range the API serves.
type window struct {
	name         string
	length       time.Duration
	res          Resolution
	minRetention int // days of hourly retention the window needs; 0 for minute windows
}

var windows = []window{
	{"15m", 15 * time.Minute, Minute, 0},
	{"1h", time.Hour, Minute, 0},
	{"6h", 6 * time.Hour, Minute, 0},
	{"24h", 24 * time.Hour, Minute, 0},
	{"7d", 7 * 24 * time.Hour, Hour, 7},
	{"30d", 30 * 24 * time.Hour, Hour, 30},
	{"90d", 90 * 24 * time.Hour, Hour, 90},
}

func (w window) bucket() time.Duration {
	if w.res == Hour {
		return time.Hour
	}
	return time.Minute
}

// Service serves request metrics (ADR-0033). The host-wide routes are left
// out of authz.Routes, so only admins reach them; the Compose route is a
// Viewer's, like the Service metrics.
type Service struct {
	store   *Store
	paths   traefikconfig.Paths
	domains *domains.Store
	db      *sql.DB
	now     func() time.Time
}

// NewService constructs the endpoints. db resolves a Compose's place in its
// Project and Environment.
func NewService(store *Store, paths traefikconfig.Paths, domains *domains.Store, db *sql.DB) *Service {
	return &Service{store: store, paths: paths, domains: domains, db: db, now: time.Now}
}

// RegisterRoutes adds the request metrics endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/requests/metrics", s.totalMetrics)
	mux.HandleFunc("GET /api/requests/panel/metrics", s.panelMetrics)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/requests/metrics", s.composeMetrics)
	mux.HandleFunc("GET /api/requests/settings", s.getSettings)
	mux.HandleFunc("PUT /api/requests/settings", s.putSettings)
}

type percentiles struct {
	P50Ms *float64 `json:"p50Ms"`
	P90Ms *float64 `json:"p90Ms"`
	P99Ms *float64 `json:"p99Ms"`
}

type summary struct {
	Requests uint64 `json:"requests"`
	C1xx     uint64 `json:"c1xx"`
	C2xx     uint64 `json:"c2xx"`
	C3xx     uint64 `json:"c3xx"`
	C4xx     uint64 `json:"c4xx"`
	C5xx     uint64 `json:"c5xx"`
	Cother   uint64 `json:"cother"`
	percentiles
}

type bucket struct {
	At     int64  `json:"at"` // epoch milliseconds
	C1xx   uint64 `json:"c1xx"`
	C2xx   uint64 `json:"c2xx"`
	C3xx   uint64 `json:"c3xx"`
	C4xx   uint64 `json:"c4xx"`
	C5xx   uint64 `json:"c5xx"`
	Cother uint64 `json:"cother"`
	percentiles
}

type envelope struct {
	Window            string    `json:"window"`
	Partial           bool      `json:"partial"`
	AccessLogEnabled  bool      `json:"accessLogEnabled"`
	AccessLogFiltered bool      `json:"accessLogFiltered"`
	RetentionDays     int       `json:"retentionDays"`
	Hosts             *[]string `json:"hosts,omitempty"` // set on the Compose route only; [] when it has no Domain
	Summary           summary   `json:"summary"`
	Buckets           []bucket  `json:"buckets"`
}

func round1(v float64) *float64 {
	r := math.Round(v*10) / 10
	return &r
}

func percentilesOf(h *Histogram) percentiles {
	var p percentiles
	if v, ok := h.Quantile(0.5); ok {
		p.P50Ms = round1(v)
	}
	if v, ok := h.Quantile(0.9); ok {
		p.P90Ms = round1(v)
	}
	if v, ok := h.Quantile(0.99); ok {
		p.P99Ms = round1(v)
	}
	return p
}

// windowOf resolves ?window= against the retention: an unknown window, or
// one the retention does not cover, is an error, never a silent default.
func windowOf(r *http.Request, retention int) (window, error) {
	name := r.URL.Query().Get("window")
	for _, w := range windows {
		if w.name != name {
			continue
		}
		if w.minRetention > retention {
			return window{}, fmt.Errorf("window %q needs a retention of at least %d days", name, w.minRetention)
		}
		return w, nil
	}
	names := make([]string, len(windows))
	for i, w := range windows {
		names[i] = w.name
	}
	return window{}, fmt.Errorf("unknown window %q: must be one of %s", name, strings.Join(names, ", "))
}

// GET /api/requests/metrics?window=1h
func (s *Service) totalMetrics(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, []Key{{Scope: ScopeTotal}}, nil)
}

// GET /api/requests/panel/metrics?window=1h
func (s *Service) panelMetrics(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, []Key{{Scope: ScopePanel}}, nil)
}

// GET /api/projects/{p}/environments/{e}/composes/{c}/requests/metrics?window=1h&host=
func (s *Service) composeMetrics(w http.ResponseWriter, r *http.Request) {
	projectID, e1 := strconv.ParseInt(r.PathValue("projectID"), 10, 64)
	environmentID, e2 := strconv.ParseInt(r.PathValue("environmentID"), 10, 64)
	composeID, e3 := strconv.ParseInt(r.PathValue("composeID"), 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || projectID <= 0 || environmentID <= 0 || composeID <= 0 {
		writeError(w, http.StatusBadRequest, "ids must be positive integers")
		return
	}
	var found int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT 1 FROM composes c JOIN environments e ON e.id = c.environment_id
		WHERE e.project_id = ? AND c.environment_id = ? AND c.id = ?`,
		projectID, environmentID, composeID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	list, err := s.domains.ListForCompose(r.Context(), composeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load domains")
		return
	}
	seen := map[string]bool{}
	hosts := []string{}
	for _, d := range list {
		h := strings.ToLower(d.Host)
		if !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)

	selected := hosts
	if host := strings.ToLower(r.URL.Query().Get("host")); host != "" {
		if !seen[host] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("host %q is not one of this compose's domains", host))
			return
		}
		selected = []string{host}
	}
	keys := make([]Key, len(selected))
	for i, h := range selected {
		keys[i] = Key{Scope: ScopeHost, ComposeID: composeID, Host: h}
	}
	s.serve(w, r, keys, hosts)
}

// serve answers a metrics request for the series in keys. hosts is the
// Compose route's selector list; nil on the host-wide routes.
func (s *Service) serve(w http.ResponseWriter, r *http.Request, keys []Key, hosts []string) {
	ctx := r.Context()
	retention, err := s.store.RetentionDays(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the retention")
		return
	}
	win, err := windowOf(r, retention)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	from := s.now().Add(-win.length).Truncate(win.bucket())
	rows, err := s.store.Rows(ctx, win.res, keys, from)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load request metrics")
		return
	}

	// One bucket per time, the series merged: histograms add, never average.
	merged := map[int64]*Counts{}
	var all Counts
	for i := range rows {
		at := rows[i].BucketAt.Unix()
		c := merged[at]
		if c == nil {
			c = &Counts{}
			merged[at] = c
		}
		c.Merge(&rows[i].Counts)
		all.Merge(&rows[i].Counts)
	}
	ats := make([]int64, 0, len(merged))
	for at := range merged {
		ats = append(ats, at)
	}
	sort.Slice(ats, func(i, j int) bool { return ats[i] < ats[j] })

	out := envelope{
		Window:        win.name,
		Partial:       int64(len(ats)) < int64(win.length/win.bucket()),
		RetentionDays: retention,
		Summary: summary{
			Requests: all.Requests(), C1xx: all.C1xx, C2xx: all.C2xx, C3xx: all.C3xx,
			C4xx: all.C4xx, C5xx: all.C5xx, Cother: all.Cother,
			percentiles: percentilesOf(&all.Latency),
		},
		Buckets: make([]bucket, 0, len(ats)),
	}
	if hosts != nil {
		out.Hosts = &hosts
	}
	if effective, err := os.ReadFile(s.paths.Effective()); err == nil {
		out.AccessLogEnabled = traefikconfig.AccessLogOn(effective)
		out.AccessLogFiltered = out.AccessLogEnabled && traefikconfig.AccessLogFiltered(effective)
	}
	for _, at := range ats {
		c := merged[at]
		out.Buckets = append(out.Buckets, bucket{
			At: at * 1000, C1xx: c.C1xx, C2xx: c.C2xx, C3xx: c.C3xx, C4xx: c.C4xx, C5xx: c.C5xx, Cother: c.Cother,
			percentiles: percentilesOf(&c.Latency),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) {
	days, err := s.store.RetentionDays(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the request metrics settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retentionDays": days})
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RetentionDays int `json:"retentionDays"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	audit.Detail(r.Context(), "retentionDays", body.RetentionDays)
	if err := s.store.SaveRetention(r.Context(), body.RetentionDays); err != nil {
		if errors.Is(err, ErrInvalidRetention) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "could not save the request metrics settings")
		return
	}
	s.getSettings(w, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
