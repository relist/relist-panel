package composes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"relist-panel/internal/zerodowntime"
)

// ZeroDowntimeStatus is one Service with a Domain: whether its switch is on
// and what would block its rollout today. Reasons never block the switch.
type ZeroDowntimeStatus struct {
	Service string   `json:"service"`
	Enabled bool     `json:"enabled"`
	Reasons []string `json:"reasons"`
}

// ZeroDowntimeServices lists every Service in the current YAML that has a
// Domain, in YAML order, checked against `docker compose config` of the
// files a deploy would write now.
func (s *Service) ZeroDowntimeServices(ctx context.Context, t DeployTarget) ([]ZeroDowntimeStatus, error) {
	services, err := s.servicesWithDomain(ctx, t.Compose)
	if err != nil || len(services) == 0 {
		return []ZeroDowntimeStatus{}, err
	}
	enabled, err := s.switches.Enabled(ctx, t.Compose.ID)
	if err != nil {
		return nil, err
	}
	on := map[string]bool{}
	for _, name := range enabled {
		on[name] = true
	}
	reasons := s.zeroDowntimeReasons(ctx, t, services)
	statuses := make([]ZeroDowntimeStatus, 0, len(services))
	for _, name := range services {
		r := reasons[name]
		if r == nil {
			r = []string{}
		}
		statuses = append(statuses, ZeroDowntimeStatus{Service: name, Enabled: on[name], Reasons: r})
	}
	return statuses, nil
}

// servicesWithDomain is the YAML's Services that have a Domain, in YAML order.
func (s *Service) servicesWithDomain(ctx context.Context, compose Compose) ([]string, error) {
	ds, err := s.domains.ListForCompose(ctx, compose.ID)
	if err != nil {
		return nil, err
	}
	hasDomain := map[string]bool{}
	for _, d := range ds {
		hasDomain[d.Service] = true
	}
	names, err := serviceNames(compose.YAMLContent)
	if err != nil {
		return nil, err
	}
	var services []string
	for _, name := range names {
		if hasDomain[name] {
			services = append(services, name)
		}
	}
	return services, nil
}

// zeroDowntimeReasons checks services the way Enqueue would. When the check
// itself cannot run, every Service gets that as its one reason.
func (s *Service) zeroDowntimeReasons(ctx context.Context, t DeployTarget, services []string) map[string][]string {
	reasons := map[string][]string{}
	problems, err := s.checkServices(ctx, t, services)
	if err != nil {
		for _, name := range services {
			reasons[name] = []string{"could not check: " + firstErrorLine(err)}
		}
		return reasons
	}
	for _, p := range problems {
		reasons[p.Service] = p.Reasons
	}
	return reasons
}

func (s *Service) checkServices(ctx context.Context, t DeployTarget, services []string) ([]zerodowntime.Problem, error) {
	captured, err := s.Capture(ctx, t)
	if err != nil {
		return nil, err
	}
	req, cleanup, err := stageCheck(captured)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	config, err := s.runner.Config(ctx, req)
	if err != nil {
		return nil, err
	}
	return zerodowntime.Check(config, services)
}

// zeroDowntimeCompose answers the shared part of both handlers: the Compose
// the route names, or false once the response is written.
func (s *Service) zeroDowntimeCompose(w http.ResponseWriter, r *http.Request) (Compose, bool) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return Compose{}, false
	}
	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return Compose{}, false
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return Compose{}, false
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return Compose{}, false
	}
	compose, err := s.repository.findByID(r.Context(), environmentID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return Compose{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return Compose{}, false
	}
	return compose, true
}

func (s *Service) listZeroDowntime(w http.ResponseWriter, r *http.Request) {
	compose, ok := s.zeroDowntimeCompose(w, r)
	if !ok {
		return
	}
	t, err := s.Target(r.Context(), compose.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	statuses, err := s.ZeroDowntimeServices(r.Context(), t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list zero-downtime services")
		return
	}
	writeJSON(w, http.StatusOK, statuses)
}

func (s *Service) setZeroDowntime(w http.ResponseWriter, r *http.Request) {
	compose, ok := s.zeroDowntimeCompose(w, r)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeInput(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	service := r.PathValue("service")
	// Turning a switch off needs no Domain, so a stale one can be cleared.
	if *input.Enabled {
		services, err := s.servicesWithDomain(r.Context(), compose)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load domains")
			return
		}
		found := false
		for _, name := range services {
			found = found || name == service
		}
		if !found {
			writeError(w, http.StatusUnprocessableEntity, service+" has no domain")
			return
		}
	}
	if err := s.switches.Set(r.Context(), compose.ID, service, *input.Enabled, s.now()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save zero-downtime switch")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
