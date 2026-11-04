// Package projects owns Project metadata and its HTTP API.
package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"relist-panel/internal/authz"
	"relist-panel/internal/composes"
	"relist-panel/internal/environments"
	"relist-panel/internal/slug"
)

const (
	maxRequestBody = 1 << 20
	maxProjectName = 255
)

type Service struct {
	repository *repository
	teardown   *composes.Service
	now        func() time.Time
}

type repository struct {
	db *sql.DB
}

// Project is the API representation of a Project.
type Project struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	EnvironmentCount int       `json:"environmentCount"`
	CreatedAt        time.Time `json:"createdAt"`
	LastUpdated      time.Time `json:"lastUpdated"`
}

type projectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// New constructs the Project service over the application's migrated SQLite
// database.
func New(db *sql.DB, teardown *composes.Service) *Service {
	return &Service{repository: &repository{db: db}, teardown: teardown, now: time.Now}
}

// RegisterRoutes adds the authenticated Project endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects", s.list)
	mux.HandleFunc("POST /api/projects", s.create)
	mux.HandleFunc("GET /api/projects/{id}", s.get)
	mux.HandleFunc("PUT /api/projects/{id}", s.update)
	mux.HandleFunc("DELETE /api/projects/{id}", s.delete)
	mux.HandleFunc("GET /api/projects/{id}/removal-plan", s.removalPlan)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "projects are temporarily unavailable")
		return
	}

	projects, err := s.repository.list(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load projects")
		return
	}

	if access, member := authz.Member(r.Context()); member {
		visible := make([]Project, 0, len(projects))
		for _, project := range projects {
			if access.ProjectVisible(project.ID) {
				project.EnvironmentCount = len(access.VisibleEnvironments(project.ID))
				visible = append(visible, project)
			}
		}
		projects = visible
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "projects are temporarily unavailable")
		return
	}

	input, err := decodeProjectInput(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateProjectName(input.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(input.Name)
	siblings, err := s.repository.list(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create project")
		return
	}
	if slug.Conflicts(name, siblings, func(p Project) string { return p.Name }) {
		writeError(w, http.StatusConflict, "a project with a conflicting name already exists")
		return
	}

	createdAt := s.now().UTC()
	project, err := s.repository.create(r.Context(), name, input.Description, createdAt)
	if err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "a project with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create project")
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "projects are temporarily unavailable")
		return
	}

	id, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	project, err := s.repository.findByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	if access, member := authz.Member(r.Context()); member {
		project.EnvironmentCount = len(access.VisibleEnvironments(project.ID))
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "projects are temporarily unavailable")
		return
	}

	id, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	input, err := decodeProjectInput(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateProjectName(input.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.repository.findByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	name := strings.TrimSpace(input.Name)
	siblings, err := s.repository.list(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update project")
		return
	}
	projectSlug := slug.Slugify(name)
	for _, sibling := range siblings {
		if sibling.ID != id && slug.Slugify(sibling.Name) == projectSlug {
			writeError(w, http.StatusConflict, "a project with a conflicting name already exists")
			return
		}
	}

	updatedAt := s.now().UTC().Truncate(time.Second)
	if !updatedAt.After(existing.LastUpdated) {
		updatedAt = existing.LastUpdated.Add(time.Second)
	}

	if err := s.repository.update(r.Context(), id, name, input.Description, updatedAt); err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "a project with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update project")
		return
	}

	project, err := s.repository.findByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load updated project")
		return
	}
	if access, member := authz.Member(r.Context()); member {
		project.EnvironmentCount = len(access.VisibleEnvironments(project.ID))
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "projects are temporarily unavailable")
		return
	}

	id, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	if s.teardown == nil {
		writeError(w, http.StatusInternalServerError, "compose teardown is unavailable")
		return
	}
	ctx, cancel := composes.LifecycleContext(r.Context())
	defer cancel()
	if err := s.teardown.TeardownProject(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, "project teardown failed: "+err.Error())
		return
	}
	deleted, err := s.repository.delete(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete project")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removalPlan reports what deleting the Project would remove from each of its
// Composes, without removing anything.
func (s *Service) removalPlan(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "projects are temporarily unavailable")
		return
	}
	id, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	if _, err := s.repository.findByID(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	if s.teardown == nil {
		writeError(w, http.StatusInternalServerError, "compose teardown is unavailable")
		return
	}
	entries, err := s.teardown.RemovalPlan(r.Context(), id, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not work out what a delete removes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"composes": entries})
}

func (s *Service) available() bool {
	return s != nil && s.repository != nil && s.repository.db != nil
}

func (r *repository) list(ctx context.Context) ([]Project, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
			(SELECT COUNT(*) FROM environments WHERE project_id = p.id)
		FROM projects AS p
		ORDER BY p.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjects(rows)
}

func (r *repository) create(ctx context.Context, name, description string, createdAt time.Time) (Project, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO projects (name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, name, description, createdAt.Unix(), createdAt.Unix())
	if err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	if err := environments.ProvisionDefault(ctx, tx, id, createdAt); err != nil {
		_ = tx.Rollback()
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return r.findByID(ctx, id)
}

func (r *repository) findByID(ctx context.Context, id int64) (Project, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
			(SELECT COUNT(*) FROM environments WHERE project_id = p.id)
		FROM projects AS p
		WHERE p.id = ?
	`, id)
	return scanProjectRow(row)
}

func (r *repository) update(ctx context.Context, id int64, name, description string, updatedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE projects
		SET name = ?, description = ?, updated_at = ?
		WHERE id = ?
	`, name, description, updatedAt.Unix(), id)
	return err
}

func (r *repository) delete(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func scanProjects(rows *sql.Rows) ([]Project, error) {
	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProjectValues(rows.Scan)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

func scanProjectRow(row *sql.Row) (Project, error) {
	return scanProjectValues(row.Scan)
}

func scanProjectValues(scan func(...any) error) (Project, error) {
	var (
		project          Project
		description      sql.NullString
		createdAt        int64
		updatedAt        int64
		environmentCount int
	)
	if err := scan(&project.ID, &project.Name, &description, &createdAt, &updatedAt, &environmentCount); err != nil {
		return Project{}, err
	}
	project.Description = description.String
	project.EnvironmentCount = environmentCount
	project.CreatedAt = time.Unix(createdAt, 0).UTC()
	project.LastUpdated = time.Unix(updatedAt, 0).UTC()
	return project, nil
}

func projectID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid project id")
	}
	return id, nil
}

func validateProjectName(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("project name is required")
	}
	if utf8.RuneCountInString(trimmed) > maxProjectName {
		return errors.New("project name must be 255 characters or fewer")
	}
	return nil
}

func decodeProjectInput(w http.ResponseWriter, r *http.Request) (projectInput, error) {
	var input projectInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return projectInput{}, errors.New("request must contain a valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return projectInput{}, errors.New("request must contain one JSON object")
	}
	return input, nil
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
