package traefikconfig

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
)

// ErrLocked wraps a Gate refusal: a Panel update or the Migration lock.
var ErrLocked = errors.New("Traefik cannot be changed now")

// Service serves the Traefik page's Configuration tab (ADR-0030).
type Service struct{ r *Runner }

func NewService(r *Runner) *Service { return &Service{r: r} }

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/traefik/config", s.get)
	mux.HandleFunc("POST /api/traefik/config/preview", s.preview)
	mux.HandleFunc("POST /api/traefik/config/test", s.test)
	mux.HandleFunc("POST /api/traefik/config/apply", s.apply)
	mux.HandleFunc("GET /api/traefik/config/apply", s.status)
	mux.HandleFunc("POST /api/traefik/config/apply/ack", s.ack)
	mux.HandleFunc("POST /api/traefik/restart", s.restart)
	mux.HandleFunc("GET /api/traefik/config/history", s.history)
	mux.HandleFunc("POST /api/traefik/config/history/{name}/restore", s.restore)
}

func readOrEmpty(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte{}, nil
	}
	return b, err
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	override, err := readOrEmpty(s.r.Paths.Override())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the Traefik override")
		return
	}
	effective, err := readOrEmpty(s.r.Paths.Effective())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read traefik.yml")
		return
	}
	warnings := []Problem{}
	var form any
	if f, ws, err := ReadForm(override); err == nil {
		form = f
		warnings = append(warnings, ws...)
	}
	var last any
	run, err := s.r.Status()
	if err != nil {
		log.Printf("traefikconfig: read apply.json: %v", err)
	}
	if run.BootstrapWarning != "" {
		warnings = append(warnings, Problem{Message: run.BootstrapWarning})
	}
	if run.ID != "" {
		last = run
	}
	pending, err := s.r.PendingRestart(r.Context())
	if err != nil {
		log.Printf("traefikconfig: pending restart: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"override": string(override), "base": string(Base()), "effective": string(effective),
		"form": form, "revision": Revision(override), "pendingRestart": pending,
		"warnings": warnings, "lastApply": last,
	})
}

// preview turns a draft (and the form, if sent) into the override and the
// file it would generate. It saves nothing and always answers 200: problems
// are part of the answer.
func (s *Service) preview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Override string `json:"override"`
		Form     *Form  `json:"form"`
	}
	if !decode(w, r, &body) {
		return
	}
	override := []byte(body.Override)
	errs := []Problem{}
	if body.Form != nil {
		out, problems, err := WriteForm(override, *body.Form)
		switch {
		case err != nil:
			errs = append(errs, ParseErrorProblem(err))
		case len(problems) > 0:
			errs = append(errs, problems...)
		default:
			override = out
		}
	}
	effective := ""
	if len(errs) == 0 {
		eff, problems, err := Generate(override)
		switch {
		case err != nil:
			errs = append(errs, ParseErrorProblem(err))
		case len(problems) > 0:
			errs = append(errs, problems...)
		default:
			effective = string(eff)
		}
	}
	warnings := []Problem{}
	var form any
	if f, ws, err := ReadForm(override); err == nil {
		form = f
		warnings = append(warnings, ws...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"override": string(override), "effective": effective, "form": form, "errors": errs, "warnings": warnings,
	})
}

func (s *Service) test(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Override string `json:"override"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, problems, err := s.r.Test(r.Context(), []byte(body.Override))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if problems == nil {
		problems = []Problem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(problems) == 0, "errors": problems, "log": res.Log})
}

func (s *Service) apply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Override string `json:"override"`
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.started(w, func(ctx context.Context) (string, error) {
		return s.r.Start(ctx, KindApply, []byte(body.Override), body.Revision)
	}, r.Context())
}

func (s *Service) restart(w http.ResponseWriter, r *http.Request) {
	s.started(w, func(ctx context.Context) (string, error) {
		return s.r.Start(ctx, KindRestart, nil, "")
	}, r.Context())
}

func (s *Service) restore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.started(w, func(ctx context.Context) (string, error) { return s.r.Restore(ctx, name) }, r.Context())
}

// started answers a run start: 202 with its id, or why it did not start.
func (s *Service) started(w http.ResponseWriter, start func(context.Context) (string, error), ctx context.Context) {
	id, err := start(ctx)
	var pe *ProblemsError
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
	case errors.As(err, &pe):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "errors": pe.Problems})
	case errors.Is(err, ErrBusy), errors.Is(err, ErrRevision):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrLocked):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrNoHistory):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		log.Printf("traefikconfig: start a run: %v", err)
		writeError(w, http.StatusInternalServerError, "could not start the Traefik apply")
	}
}

func (s *Service) status(w http.ResponseWriter, _ *http.Request) {
	run, err := s.r.Status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the last Traefik apply")
		return
	}
	if run.ID == "" {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Service) ack(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.r.Ack(body.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the acknowledgement")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) history(w http.ResponseWriter, _ *http.Request) {
	entries, err := s.r.History()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list the Traefik history")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
