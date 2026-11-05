package deployments

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/authz"
)

// GlobalQuery selects retained Deployments across all Composes. Page and
// PageSize are positive; the HTTP layer validates them before querying.
type GlobalQuery struct {
	Page, PageSize                      int
	ProjectID, EnvironmentID, ComposeID int64
	Status                              Status
	From, To                            *time.Time
	// EnvironmentIDs limits the list to these Environments: a member's
	// visible ones. Nil lists all; empty lists none.
	EnvironmentIDs []int64
}

// GlobalDeployment carries the current context needed to identify a
// Compose-local Deployment number and open its existing detail routes.
type GlobalDeployment struct {
	Deployment
	ProjectID       int64
	ProjectName     string
	EnvironmentID   int64
	EnvironmentName string
	ComposeName     string
	Ahead           *int
}

const globalFrom = ` FROM deployments d
JOIN composes c ON c.id = d.compose_id
JOIN environments e ON e.id = c.environment_id
JOIN projects p ON p.id = e.project_id`

// time.Parse accepts non-RFC3339 forms such as a single-digit hour, a comma
// fraction, and out-of-range zone offsets. Check the grammar before parsing.
var globalTimestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(\.\d+)?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)

// ListGlobal reads the count, page and queue positions in one database snapshot.
// It does not change retention or the host queue's scheduling.
func (s *Store) ListGlobal(ctx context.Context, q GlobalQuery) ([]GlobalDeployment, int, error) {
	where := []string{}
	args := []any{}
	add := func(predicate string, value any) {
		where = append(where, predicate)
		args = append(args, value)
	}
	if q.ProjectID != 0 {
		add("p.id = ?", q.ProjectID)
	}
	if q.EnvironmentID != 0 {
		add("e.id = ?", q.EnvironmentID)
	}
	if q.ComposeID != 0 {
		add("c.id = ?", q.ComposeID)
	}
	if q.Status != "" {
		add("d.status = ?", string(q.Status))
	}
	if q.From != nil {
		// queued_at stores whole seconds. A fractional lower bound must round
		// up to avoid including an instant strictly before the requested range.
		second := q.From.Unix()
		if q.From.Nanosecond() != 0 {
			second++
		}
		add("d.queued_at >= ?", second)
	}
	if q.To != nil {
		add("d.queued_at <= ?", q.To.Unix())
	}
	if q.EnvironmentIDs != nil {
		if len(q.EnvironmentIDs) == 0 {
			where = append(where, "0")
		} else {
			where = append(where, "e.id IN (?"+strings.Repeat(", ?", len(q.EnvironmentIDs)-1)+")")
			for _, id := range q.EnvironmentIDs {
				args = append(args, id)
			}
		}
	}
	from := globalFrom
	if len(where) != 0 {
		from += " WHERE " + strings.Join(where, " AND ")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	// Qualify the shared deployment columns so the joins' IDs and names cannot
	// shadow Deployment fields or drift from the existing JSON conversion.
	selected := strings.Split(columns, ",")
	for i, column := range selected {
		selected[i] = "d." + strings.TrimSpace(column)
	}
	selected = append(selected, "p.id", "p.name", "e.id", "e.name", "c.name")
	pageArgs := append(append([]any{}, args...), q.PageSize, (q.Page-1)*q.PageSize)
	rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(selected, ", ")+from+" ORDER BY d.queued_at DESC, d.id DESC LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	items := make([]GlobalDeployment, 0)
	for rows.Next() {
		var item GlobalDeployment
		item.Deployment, err = scan(rows, &item.ProjectID, &item.ProjectName, &item.EnvironmentID, &item.EnvironmentName, &item.ComposeName)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	for i := range items {
		if items[i].Status == StatusQueued {
			n, err := ahead(ctx, tx, items[i].Deployment)
			if err != nil {
				return nil, 0, err
			}
			items[i].Ahead = &n
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

type globalDeploymentJSON struct {
	deploymentJSON
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	ComposeName     string `json:"composeName"`
}

type globalResponse struct {
	Items    []globalDeploymentJSON `json:"items"`
	Total    int                    `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"pageSize"`
}

func (a *API) globalList(w http.ResponseWriter, r *http.Request) {
	q, err := parseGlobalQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if access, member := authz.Member(r.Context()); member {
		q.EnvironmentIDs = access.AllVisibleEnvironments()
	}
	items, total, err := a.q.d.Store.ListGlobal(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list deployments")
		return
	}
	out := make([]globalDeploymentJSON, 0, len(items))
	for _, item := range items {
		j := toJSON(item.Deployment)
		j.Ahead = item.Ahead
		out = append(out, globalDeploymentJSON{deploymentJSON: j,
			ProjectID: item.ProjectID, ProjectName: item.ProjectName,
			EnvironmentID: item.EnvironmentID, EnvironmentName: item.EnvironmentName,
			ComposeName: item.ComposeName})
	}
	writeJSON(w, http.StatusOK, globalResponse{Items: out, Total: total, Page: q.Page, PageSize: q.PageSize})
}

func parseGlobalQuery(raw string) (GlobalQuery, error) {
	q := GlobalQuery{Page: 1, PageSize: 20}
	var fromFraction, toFraction string
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, fmt.Errorf("invalid query parameters")
	}
	for key, all := range values {
		if len(all) != 1 || all[0] == "" {
			return q, fmt.Errorf("%s must have one non-empty value", key)
		}
		value := all[0]
		switch key {
		case "page", "pageSize", "projectId", "environmentId", "composeId":
			n, err := positiveQueryInteger(value)
			if err != nil {
				return q, fmt.Errorf("%s must be a positive integer", key)
			}
			switch key {
			case "page":
				if n > int64(int(^uint(0)>>1)) {
					return q, fmt.Errorf("page is too large")
				}
				q.Page = int(n)
			case "pageSize":
				if n > 100 {
					return q, fmt.Errorf("pageSize must not exceed 100")
				}
				q.PageSize = int(n)
			case "projectId":
				q.ProjectID = n
			case "environmentId":
				q.EnvironmentID = n
			case "composeId":
				q.ComposeID = n
			}
		case "status":
			switch Status(value) {
			case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCanceled:
				q.Status = Status(value)
			default:
				return q, fmt.Errorf("status must be queued, running, succeeded, failed or canceled")
			}
		case "from", "to":
			at, err := time.Parse(time.RFC3339, value)
			if err != nil || !globalTimestampPattern.MatchString(value) {
				return q, fmt.Errorf("%s must be an RFC3339 timestamp", key)
			}
			if key == "from" {
				q.From = &at
				fromFraction = timestampFraction(value)
			} else {
				q.To = &at
				toFraction = timestampFraction(value)
			}
		default:
			return q, fmt.Errorf("unknown query parameter %s", key)
		}
	}
	if q.From != nil && q.To != nil && (q.From.After(*q.To) ||
		(q.From.Equal(*q.To) && fromFraction > toFraction)) {
		return q, fmt.Errorf("from must not be after to")
	}
	// RFC3339 permits fractions beyond time.Time's nanosecond precision. Keep
	// a positive fraction from becoming an exact second before ListGlobal rounds
	// the inclusive lower bound up to the next stored whole second.
	if q.From != nil && q.From.Nanosecond() == 0 && fromFraction != "" {
		at := q.From.Add(time.Nanosecond)
		q.From = &at
	}
	if q.Page-1 > int(^uint(0)>>1)/q.PageSize {
		return q, fmt.Errorf("page is too large")
	}
	return q, nil
}

// Removing trailing zeroes makes lexical ordering of these decimal fractions
// exact even when time.Parse has truncated digits after the ninth place.
func timestampFraction(value string) string {
	if value[19] != '.' {
		return ""
	}
	end := 20
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	return strings.TrimRight(value[20:end], "0")
}

func positiveQueryInteger(value string) (int64, error) {
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not an integer")
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("not a positive integer")
	}
	return n, nil
}
