package databases

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/backupdest"
	"relist-panel/internal/schedule"
)

// ScheduleJSON is a Database's backup schedule as the browser sees it. A keep
// value is a number, or null for no limit.
type ScheduleJSON struct {
	Enabled         bool       `json:"enabled"`
	Days            []string   `json:"days"`
	Minute          int        `json:"minute"`
	All             bool       `json:"all"`
	Databases       []string   `json:"databases"`
	LocalKeepCount  *int       `json:"localKeepCount"`
	LocalKeepDays   *int       `json:"localKeepDays"`
	RemoteKeepCount *int       `json:"remoteKeepCount"`
	RemoteKeepDays  *int       `json:"remoteKeepDays"`
	DestinationID   *int64     `json:"destinationId"`
	LastRunAt       *time.Time `json:"lastRunAt"`
	LastError       string     `json:"lastError"`
}

// scheduleReply is what GET and PUT answer: the schedule, when it runs next
// and the time zone its time is in.
type scheduleReply struct {
	ScheduleJSON
	Next     *time.Time `json:"next"`
	TimeZone string     `json:"timeZone"`
}

func (a *API) scheduleReply(sc Schedule) scheduleReply {
	now := a.s.d.Now()
	out := scheduleReply{ScheduleJSON: ScheduleJSON{Enabled: sc.Enabled, Days: sc.Days, Minute: sc.Minute, All: sc.All,
		Databases: sc.Databases, LocalKeepCount: sc.Local.Count, LocalKeepDays: sc.Local.Days,
		RemoteKeepCount: sc.Remote.Count, RemoteKeepDays: sc.Remote.Days, DestinationID: sc.DestinationID, LastRunAt: sc.LastRunAt, LastError: sc.LastError},
		TimeZone: schedule.TimeZone(now)}
	if sc.Enabled {
		out.Next = schedule.Next(now, ruleOf(sc))
	}
	return out
}

func (a *API) getSchedule(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	sc, err := a.s.GetSchedule(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.scheduleReply(sc))
}

func (a *API) putSchedule(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in ScheduleJSON
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	sc := Schedule{DatabaseID: n, Enabled: in.Enabled, Days: in.Days, Minute: in.Minute, All: in.All, Databases: in.Databases,
		Local: Keep{Count: in.LocalKeepCount, Days: in.LocalKeepDays}, Remote: Keep{Count: in.RemoteKeepCount, Days: in.RemoteKeepDays}, DestinationID: in.DestinationID}
	saved, err := a.s.SaveSchedule(r.Context(), sc)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.scheduleReply(saved))
}

func (a *API) retryUpload(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	bid, ok := backupID(w, r)
	if !ok {
		return
	}
	a.target(r, n)
	if err := a.s.RetryUpload(r.Context(), n, bid); err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "backup", bid)
	w.WriteHeader(http.StatusAccepted)
}

func (a *API) pinBackup(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	bid, ok := backupID(w, r)
	if !ok {
		return
	}
	var in struct {
		Pinned bool `json:"pinned"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	if err := a.s.PinBackup(r.Context(), n, bid, in.Pinned); err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "backup", bid)
	w.WriteHeader(http.StatusNoContent)
}

// ErrInvalidSchedule is a schedule the admin must fix; the HTTP answer is 422.
type ErrInvalidSchedule struct{ Msg string }

func (e ErrInvalidSchedule) Error() string { return e.Msg }

// scheduleDatabase checks that Database id is live and has Backups.
func (s *Service) scheduleDatabase(ctx context.Context, id int64) error {
	if err := s.hasBackups(ctx, id); err != nil {
		return err
	}
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.DeletedAt != nil {
		return ErrNotFound
	}
	return nil
}

// GetSchedule returns Database id's schedule, the defaults when it has none.
func (s *Service) GetSchedule(ctx context.Context, id int64) (Schedule, error) {
	if err := s.scheduleDatabase(ctx, id); err != nil {
		return Schedule{}, err
	}
	return s.d.Store.GetSchedule(ctx, id)
}

// SaveSchedule checks and stores sc, and returns what is stored. A tombstone
// or a Redis Database cannot have a schedule.
func (s *Service) SaveSchedule(ctx context.Context, sc Schedule) (Schedule, error) {
	if err := s.scheduleDatabase(ctx, sc.DatabaseID); err != nil {
		return Schedule{}, err
	}
	days, err := schedule.NormalizeDays(sc.Days)
	if err != nil {
		return Schedule{}, ErrInvalidSchedule{err.Error()}
	}
	sc.Days = days
	if sc.Minute < 0 || sc.Minute > 1439 {
		return Schedule{}, ErrInvalidSchedule{"the time must be between 00:00 and 23:59"}
	}
	for _, v := range []*int{sc.Local.Count, sc.Local.Days, sc.Remote.Count, sc.Remote.Days} {
		if v != nil && *v < 1 {
			return Schedule{}, ErrInvalidSchedule{"keep at least 1"}
		}
	}
	if sc.All {
		sc.Databases = []string{}
	} else {
		if len(sc.Databases) == 0 {
			return Schedule{}, ErrNoScope
		}
		for _, n := range sc.Databases {
			if err := checkName(n); err != nil {
				return Schedule{}, err
			}
		}
		slices.Sort(sc.Databases)
		sc.Databases = slices.Compact(sc.Databases)
	}
	if sc.DestinationID != nil {
		if s.d.Destinations == nil {
			return Schedule{}, ErrInvalidSchedule{ErrNoDestinations.Error()}
		}
		if _, _, err := s.d.Destinations.Open(ctx, *sc.DestinationID); errors.Is(err, backupdest.ErrNotFound) {
			return Schedule{}, ErrInvalidSchedule{"no such Backup destination"}
		} else if err != nil {
			return Schedule{}, err
		}
	}
	if err := s.d.Store.SaveSchedule(ctx, sc); err != nil {
		return Schedule{}, err
	}
	return s.d.Store.GetSchedule(ctx, sc.DatabaseID)
}

// PinBackup marks a scheduled Backup as kept by Rotation, or not.
func (s *Service) PinBackup(ctx context.Context, id, backupID int64, pinned bool) error {
	if err := s.hasBackups(ctx, id); err != nil {
		return err
	}
	b, err := s.d.Store.GetBackup(ctx, id, backupID)
	if err != nil {
		return err
	}
	if b.Trigger != TriggerScheduled {
		return ErrNotScheduled
	}
	return s.d.Store.SetPinned(ctx, backupID, pinned)
}
