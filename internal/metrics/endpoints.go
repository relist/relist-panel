package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"relist-panel/internal/hostmetrics"
)

// HostHistory is the read side of the host sampler — the one-hour ring and
// the sources availability map. Declared here, in the consumer, per
// ADR-0003: *hostmetrics.Sampler satisfies it, and the HTTP-seam tests
// inject a fake so no test reads the real /proc.
type HostHistory interface {
	// Window copies the sampler's most recent n samples, chronological.
	Window(n int) []hostmetrics.HostSample
	// Sources snapshots what was readable at the last tick, and why not.
	Sources() map[string]hostmetrics.SourceStatus
}

// ServiceFacts is the read side of the container collector: the status rail's
// per-Service rows, the Monitor tab's facts, and sub-hour Service windows.
type ServiceFacts interface {
	// Services snapshots the latest sample per Service.
	Services() []hostmetrics.ServiceSample
	// ServiceWindow copies one Service's last n sweeps, chronological.
	ServiceWindow(project, service string, n int) []hostmetrics.ServiceSample
	// Status reports the last sweep's outcome.
	Status() hostmetrics.SourceStatus
}

// Service serves the monitoring endpoints, registered on the same mux as
// every other route so the existing auth middleware covers them. Windows are
// served at their natural resolution — the ring up to an hour, the 1-minute
// rollups beyond — and downsampling stays in the chart, which applies the
// min/max rule to the plot's pixel width.
type Service struct {
	store   *Store
	history HostHistory
	facts   ServiceFacts
	now     func() time.Time
}

// New constructs the metrics endpoints over the rollup store and the two
// sampler-side read ports.
func New(store *Store, history HostHistory, facts ServiceFacts) *Service {
	return &Service{store: store, history: history, facts: facts, now: time.Now}
}

// RegisterRoutes adds the authenticated metrics endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/host/metrics", s.hostMetrics)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/services/{service}/metrics", s.serviceMetrics)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/services", s.services)
}

// windows are the design's four, exactly. Anything else — including a
// missing window parameter — is a 400, never a silent default: a chart that
// asked for a day and quietly got an hour would lie worse than an error.
var windows = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
}

func windowOf(r *http.Request) (time.Duration, error) {
	requested := r.URL.Query().Get("window")
	length, ok := windows[requested]
	if !ok {
		return 0, fmt.Errorf("unknown window %q: must be one of 15m, 1h, 6h, 24h", requested)
	}
	return length, nil
}

// GET /api/host/metrics?window=1h
func (s *Service) hostMetrics(w http.ResponseWriter, r *http.Request) {
	window, err := windowOf(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		from     string
		interval time.Duration
		samples  []hostmetrics.HostSample
	)
	if window <= time.Hour {
		from, interval = fromRing, hostmetrics.DefaultInterval
		samples = s.history.Window(int(window / interval))
	} else {
		from, interval = fromRollups, time.Minute
		if samples, err = s.store.HostRollups(r.Context(), s.now().Add(-window)); err != nil {
			writeError(w, http.StatusInternalServerError, "could not load host rollups")
			return
		}
	}

	writeJSON(w, http.StatusOK, envelope{
		Window:          r.URL.Query().Get("window"),
		From:            from,
		IntervalSeconds: int(interval.Seconds()),
		Partial:         len(samples) < int(window/interval),
		Sources:         wireSources(s.history.Sources()),
		Samples:         hostSamplesWire(samples),
	})
}

// GET /api/projects/{p}/environments/{e}/composes/{c}/services/{service}/metrics?window=1h
//
// A Service the collector has never swept is not a 404 — its rollups are its
// past, and an empty sub-hour window with `partial` is the honest answer.
func (s *Service) serviceMetrics(w http.ResponseWriter, r *http.Request) {
	window, err := windowOf(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID, environmentID, composeID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := s.store.ComposeProjectName(r.Context(), projectID, environmentID, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	service := r.PathValue("service")

	var (
		from     string
		interval time.Duration
		samples  []hostmetrics.ServiceSample
	)
	if window <= time.Hour {
		from, interval = fromRing, hostmetrics.SweepInterval
		samples = s.facts.ServiceWindow(project, service, int(window/interval))
	} else {
		from, interval = fromRollups, time.Minute
		if samples, err = s.store.ServiceRollups(r.Context(), project, service, s.now().Add(-window)); err != nil {
			writeError(w, http.StatusInternalServerError, "could not load service rollups")
			return
		}
	}

	writeJSON(w, http.StatusOK, envelope{
		Window:          r.URL.Query().Get("window"),
		From:            from,
		IntervalSeconds: int(interval.Seconds()),
		Partial:         len(samples) < int(window/interval),
		// The Service side has exactly one source: the stats API sweep.
		Sources: map[string]hostmetrics.SourceStatus{"dockerStats": s.facts.Status()},
		Samples: serviceSamplesWire(samples),
	})
}

// GET /api/projects/{p}/environments/{e}/composes/{c}/services — the status
// rail's and the facts strip's per-Service rows, straight from the
// collector's cache. 404 for a Compose that does not exist; an existing
// Compose with nothing running is an empty list, which is the truth.
func (s *Service) services(w http.ResponseWriter, r *http.Request) {
	projectID, environmentID, composeID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := s.store.ComposeProjectName(r.Context(), projectID, environmentID, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}

	facts := make([]serviceFacts, 0)
	for _, sample := range s.facts.Services() {
		if sample.Project != project {
			continue
		}
		facts = append(facts, serviceFacts{
			Service:       sample.Service,
			ContainerID:   sample.ContainerID,
			ContainerName: sample.Name,
			Image:         sample.Image,
			State:         sample.State,
			Health:        sample.Health,
			Ports:         portsWire(sample.Ports),
		})
	}
	writeJSON(w, http.StatusOK, facts)
}

func routeIDs(r *http.Request) (projectID, environmentID, composeID int64, err error) {
	if projectID, err = parsePositiveID(r.PathValue("projectID")); err != nil {
		return 0, 0, 0, errors.New("project id must be a positive integer")
	}
	if environmentID, err = parsePositiveID(r.PathValue("environmentID")); err != nil {
		return 0, 0, 0, errors.New("environment id must be a positive integer")
	}
	if composeID, err = parsePositiveID(r.PathValue("composeID")); err != nil {
		return 0, 0, 0, errors.New("compose id must be a positive integer")
	}
	return projectID, environmentID, composeID, nil
}

func parsePositiveID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}
