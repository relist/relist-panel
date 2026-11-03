package vulnscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// settingsTimeout bounds a settings save, which may pull a Scanner image.
const settingsTimeout = 2 * time.Minute

// staleAfter is how old the DB may get before the status turns yellow.
const staleAfter = 7 * 24 * time.Hour

var dailyTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/vulnerabilities"
	mux.HandleFunc("GET "+base+"/settings", s.getSettings)
	mux.HandleFunc("PUT "+base+"/settings", s.putSettings)
	mux.HandleFunc("GET "+base+"/scanner-tags", s.getScannerTags)
	mux.HandleFunc("GET "+base+"/status", s.getStatus)
	mux.HandleFunc("POST "+base+"/scans", s.postScans)
	mux.HandleFunc("POST "+base+"/db-update", s.postDBUpdate)
	mux.HandleFunc("DELETE "+base+"/cache", s.deleteCache)
	mux.HandleFunc("GET "+base+"/images", s.getImages)
	mux.HandleFunc("GET "+base+"/images/{id}/findings", s.getFindings)
	mux.HandleFunc("GET "+base+"/cves", s.getCVEs)
	mux.HandleFunc("GET "+base+"/summary", s.getSummary)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type settingsBody struct {
	Enabled      bool   `json:"enabled"`
	AfterDeploy  bool   `json:"afterDeploy"`
	Daily        bool   `json:"daily"`
	DailyTime    string `json:"dailyTime"`
	ScannerImage string `json:"scannerImage"`
}

type settingsResponse struct {
	settingsBody
	DefaultScannerImage string `json:"defaultScannerImage"`
	ActiveImage         string `json:"activeImage"`
}

func toSettings(st Settings) settingsResponse {
	return settingsResponse{
		settingsBody: settingsBody{
			Enabled: st.Enabled, AfterDeploy: st.AfterDeploy, Daily: st.Daily,
			DailyTime:    fmt.Sprintf("%02d:%02d", st.DailyMinute/60, st.DailyMinute%60),
			ScannerImage: st.ScannerImage,
		},
		DefaultScannerImage: DefaultScannerImage,
		ActiveImage:         st.ActiveImage,
	}
}

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan settings")
		return
	}
	writeJSON(w, http.StatusOK, toSettings(st))
}

// putSettings applies a Scanner image change first, then the master switch,
// then the rest, so a failed pull changes nothing it would depend on.
func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m := dailyTimePattern.FindStringSubmatch(body.DailyTime)
	if m == nil {
		writeError(w, http.StatusUnprocessableEntity, "Daily time must be HH:MM")
		return
	}
	if body.ScannerImage != "" && !ValidScannerRef(body.ScannerImage) {
		writeError(w, http.StatusUnprocessableEntity, ErrInvalidScannerImage.Error())
		return
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	ctx, cancel := context.WithTimeout(r.Context(), settingsTimeout)
	defer cancel()

	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan settings")
		return
	}
	if body.ScannerImage != st.ScannerImage {
		if err := s.SetScannerImage(ctx, body.ScannerImage); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	if body.Enabled != st.Enabled {
		change := s.Disable
		if body.Enabled {
			change = s.Enable
		}
		if err := change(ctx); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	s.mu.Lock()
	st, err = s.d.Store.Settings(ctx)
	if err == nil {
		st.AfterDeploy, st.Daily, st.DailyMinute = body.AfterDeploy, body.Daily, hour*60+minute
		err = s.d.Store.SaveSettings(ctx, st)
	}
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save scan settings")
		return
	}
	writeJSON(w, http.StatusOK, toSettings(st))
}

func (s *Service) getScannerTags(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan settings")
		return
	}
	tags, err := s.scannerTags(r.Context(), st.EffectiveImage())
	if errors.Is(err, errNotHub) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

type runningResponse struct {
	Kind     TaskKind `json:"kind"`
	ImageID  string   `json:"imageId,omitempty"`
	ImageRef string   `json:"imageRef,omitempty"`
}

type dbResponse struct {
	Version   string     `json:"version"`
	UpdatedAt *time.Time `json:"updatedAt"`
	Stale     bool       `json:"stale"`
	Error     string     `json:"error"`
}

func (s *Service) getStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan settings")
		return
	}
	running, queued, err := s.d.Store.QueueState(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the scan queue")
		return
	}
	now := s.d.Now()
	var run *runningResponse
	if running != nil {
		run = &runningResponse{Kind: running.Kind}
		if running.Kind == KindImage {
			run.ImageID, run.ImageRef = running.ImageID, s.displayRef(ctx, running.ImageID)
		}
	}
	cache, err := s.d.Scanner.CacheSize(ctx)
	if err != nil {
		logf("read the cache size: %v", err)
	}
	zone, offset := now.Zone()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": st.Enabled,
		"running": run,
		"queued":  queued,
		"db": dbResponse{
			Version: st.DBVersion, UpdatedAt: st.DBUpdatedAt, Error: st.DBError,
			Stale: st.DBError != "" || (st.DBUpdatedAt != nil && now.Sub(*st.DBUpdatedAt) > staleAfter),
		},
		"lastDailyAt": st.LastDailyAt,
		"nextDailyAt": NextDaily(now, st),
		"timeZone":    fmt.Sprintf("%s (UTC%+03d:%02d)", zone, offset/3600, abs(offset%3600)/60),
		"cacheBytes":  cache,
	})
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (s *Service) postScans(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ImageID string `json:"imageId"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	var queued int
	var err error
	if body.ImageID != "" {
		queued, err = 1, s.ScanImage(r.Context(), body.ImageID, ReasonManual)
	} else {
		queued, err = s.ScanAll(r.Context(), ReasonManual)
	}
	if errors.Is(err, ErrDisabled) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not queue the scan")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"queued": queued})
}

func (s *Service) postDBUpdate(w http.ResponseWriter, r *http.Request) {
	err := s.UpdateDB(r.Context())
	if errors.Is(err, ErrDisabled) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not queue the DB update")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) deleteCache(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan settings")
		return
	}
	if st.Enabled {
		writeError(w, http.StatusConflict, "Turn scanning off before deleting the cache")
		return
	}
	if err := s.d.Scanner.DeleteCache(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type imageResponse struct {
	ImageID     string      `json:"imageId"`
	Refs        []string    `json:"refs"`
	Users       []ImageUser `json:"users"`
	OS          string      `json:"os"`
	Counts      *Counts     `json:"counts"`
	ScannedAt   *time.Time  `json:"scannedAt"`
	AttemptedAt *time.Time  `json:"attemptedAt"`
	Error       string      `json:"error"`
}

func (s *Service) getImages(w http.ResponseWriter, r *http.Request) {
	used, err := s.UsedImages(r.Context())
	if err != nil {
		usedImagesFailed(w, err)
		return
	}
	results, err := s.d.Store.ImageResults(r.Context(), imageIDs(used))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan results")
		return
	}
	out := make([]imageResponse, 0, len(used))
	for _, image := range used {
		row := imageResponse{ImageID: image.ImageID, Refs: image.Refs, Users: image.Users}
		if res, ok := results[image.ImageID]; ok {
			row.OS, row.ScannedAt, row.AttemptedAt, row.Error = res.OS, res.ScannedAt, res.AttemptedAt, res.Error
			if res.ScannedAt != nil {
				counts := res.Counts
				row.Counts = &counts
			}
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// usedImagesFailed answers a failed UsedImages. The cause is logged: the
// answer names no cause, and it can be Docker or the database.
func usedImagesFailed(w http.ResponseWriter, err error) {
	logf("list the images in use: %v", err)
	writeError(w, http.StatusBadGateway, "could not list the images in use")
}

func imageIDs(used []UsedImage) []string {
	ids := make([]string, 0, len(used))
	for _, image := range used {
		ids = append(ids, image.ImageID)
	}
	return ids
}

type findingResponse struct {
	VulnID           string    `json:"vulnId"`
	PkgName          string    `json:"pkgName"`
	PkgPath          string    `json:"pkgPath"`
	InstalledVersion string    `json:"installedVersion"`
	FixedVersion     string    `json:"fixedVersion"`
	Severity         Severity  `json:"severity"`
	Title            string    `json:"title"`
	URL              string    `json:"url"`
	FirstSeenAt      time.Time `json:"firstSeenAt"`
	New              bool      `json:"new"`
}

func (s *Service) getFindings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.d.Store.Findings(r.Context(), r.PathValue("id"), s.d.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read findings")
		return
	}
	out := make([]findingResponse, 0, len(rows))
	for _, f := range rows {
		out = append(out, findingResponse{
			VulnID: f.VulnID, PkgName: f.PkgName, PkgPath: f.PkgPath, InstalledVersion: f.InstalledVersion,
			FixedVersion: f.FixedVersion, Severity: f.Severity, Title: f.Title, URL: f.URL,
			FirstSeenAt: f.FirstSeenAt, New: f.New,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// scope resolves ?projectId= or ?composeId= to the images in use there, all
// images in use when neither is given.
func (s *Service) scope(r *http.Request) (used []UsedImage, composeID int64, bad bool, err error) {
	q := r.URL.Query()
	var projectID int64
	for name, dst := range map[string]*int64{"projectId": &projectID, "composeId": &composeID} {
		if v := q.Get(name); v != "" {
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil || n <= 0 {
				return nil, 0, true, nil
			}
			*dst = n
		}
	}
	all, err := s.UsedImages(r.Context())
	if err != nil || (projectID == 0 && composeID == 0) {
		return all, 0, false, err
	}
	for _, image := range all {
		for _, u := range image.Users {
			if u.Kind == UserService && (composeID == 0 || u.ComposeID == composeID) && (projectID == 0 || u.ProjectID == projectID) {
				used = append(used, image)
				break
			}
		}
	}
	return used, composeID, false, nil
}

type cveResponse struct {
	VulnID      string    `json:"vulnId"`
	Severity    Severity  `json:"severity"`
	Title       string    `json:"title"`
	Fixable     bool      `json:"fixable"`
	FirstSeenAt time.Time `json:"firstSeenAt"`
	New         bool      `json:"new"`
	ImageIDs    []string  `json:"imageIds"`
}

func (s *Service) getCVEs(w http.ResponseWriter, r *http.Request) {
	used, _, bad, err := s.scope(r)
	q := r.URL.Query()
	query := CVEQuery{ImageIDs: imageIDs(used), Q: strings.TrimSpace(q.Get("q")), Page: 1, PageSize: 50,
		FixableOnly: truthy(q.Get("fixable")), NewOnly: truthy(q.Get("new"))}
	for name, dst := range map[string]*int{"page": &query.Page, "pageSize": &query.PageSize} {
		if v := q.Get(name); v != "" {
			n, perr := strconv.Atoi(v)
			if perr != nil || n <= 0 {
				bad = true
			}
			*dst = n
		}
	}
	if bad {
		writeError(w, http.StatusBadRequest, "invalid query")
		return
	}
	if err != nil {
		usedImagesFailed(w, err)
		return
	}
	query.PageSize = min(query.PageSize, 200)
	for _, sev := range strings.Split(q.Get("severity"), ",") {
		if sev = strings.ToUpper(strings.TrimSpace(sev)); sev != "" {
			query.Severities = append(query.Severities, Severity(sev))
		}
	}
	rows, total, err := s.d.Store.CVEs(r.Context(), query, s.d.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read CVEs")
		return
	}
	items := make([]cveResponse, 0, len(rows))
	for _, c := range rows {
		items = append(items, cveResponse(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func truthy(v string) bool { return v == "1" || v == "true" }

type serviceCounts struct {
	Service string `json:"service"`
	ImageID string `json:"imageId"`
	Counts  Counts `json:"counts"`
}

func (s *Service) getSummary(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read scan settings")
		return
	}
	if !st.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	used, composeID, bad, err := s.scope(r)
	if bad {
		writeError(w, http.StatusBadRequest, "invalid query")
		return
	}
	if err != nil {
		usedImagesFailed(w, err)
		return
	}
	counts, err := s.d.Store.Counts(r.Context(), imageIDs(used))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not count CVEs")
		return
	}
	out := map[string]any{"enabled": true, "counts": counts}
	if composeID != 0 {
		services := []serviceCounts{}
		for _, image := range used {
			c, err := s.d.Store.Counts(r.Context(), []string{image.ImageID})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "could not count CVEs")
				return
			}
			for _, u := range image.Users {
				if u.Kind == UserService && u.ComposeID == composeID {
					services = append(services, serviceCounts{Service: u.Service, ImageID: image.ImageID, Counts: c})
				}
			}
		}
		out["services"] = services
	}
	writeJSON(w, http.StatusOK, out)
}
