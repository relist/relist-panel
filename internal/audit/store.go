package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PageSize is how many events one List call returns.
const PageSize = 100

// ErrInvalidRetention is a negative retention.
var ErrInvalidRetention = errors.New("retention must be at least 1 day, or 0 to keep events forever")

// Execer writes; *sql.DB and *sql.Tx satisfy it.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Querier reads; *sql.DB and *sql.Tx satisfy it.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Record stores one event.
func Record(ctx context.Context, db Execer, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	var detail any
	if len(e.Detail) > 0 {
		body, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = string(body)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO audit_events
		(at, actor_user_id, actor, action, outcome, project_id, environment_id, compose_id, target, detail, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.UnixMilli(), nullID(e.ActorUserID), e.Actor, e.Action, e.Outcome,
		nullID(e.ProjectID), nullID(e.EnvironmentID), nullID(e.ComposeID), e.Target, detail, nullString(e.IP))
	return err
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Filter narrows List. Zero fields do not filter. With EnvironmentIDs
// non-nil, only events of those Environments are kept, plus the Project's
// own (no Environment) when ProjectOwn.
type Filter struct {
	Cursor         int64
	Actor          string
	Action         string
	From, To       int64
	ProjectID      int64
	EnvironmentIDs []int64
	ProjectOwn     bool
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// List answers one page, newest first, and the cursor of the next page
// (0 on the last).
func List(ctx context.Context, q Querier, f Filter) ([]Row, int64, error) {
	var where []string
	var args []any
	if f.Cursor > 0 {
		where, args = append(where, "id < ?"), append(args, f.Cursor)
	}
	if f.Actor != "" {
		where, args = append(where, "actor = ? COLLATE NOCASE"), append(args, f.Actor)
	}
	if f.Action != "" {
		where, args = append(where, `action LIKE ? ESCAPE '\'`), append(args, likeEscaper.Replace(f.Action)+"%")
	}
	if f.From > 0 {
		where, args = append(where, "at >= ?"), append(args, f.From)
	}
	if f.To > 0 {
		where, args = append(where, "at <= ?"), append(args, f.To)
	}
	if f.ProjectID > 0 {
		where, args = append(where, "project_id = ?"), append(args, f.ProjectID)
	}
	if f.EnvironmentIDs != nil {
		var parts []string
		if len(f.EnvironmentIDs) > 0 {
			marks := strings.TrimSuffix(strings.Repeat("?,", len(f.EnvironmentIDs)), ",")
			parts = append(parts, "environment_id IN ("+marks+")")
			for _, id := range f.EnvironmentIDs {
				args = append(args, id)
			}
		}
		if f.ProjectOwn {
			parts = append(parts, "environment_id IS NULL")
		}
		if len(parts) == 0 {
			parts = append(parts, "0")
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	query := `SELECT id, at, actor_user_id, actor, action, outcome, project_id, environment_id, compose_id, target, detail, ip
		FROM audit_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, PageSize+1)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var row Row
		var actorID, projectID, environmentID, composeID sql.NullInt64
		var detail, ip sql.NullString
		if err := rows.Scan(&row.ID, &row.At, &actorID, &row.Actor, &row.Action, &row.Outcome,
			&projectID, &environmentID, &composeID, &row.Target, &detail, &ip); err != nil {
			return nil, 0, err
		}
		row.ActorUserID, row.ProjectID = ptr(actorID), ptr(projectID)
		row.EnvironmentID, row.ComposeID = ptr(environmentID), ptr(composeID)
		if detail.Valid {
			if err := json.Unmarshal([]byte(detail.String), &row.Detail); err != nil {
				return nil, 0, err
			}
		}
		if ip.Valid {
			row.IP = &ip.String
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > PageSize {
		out = out[:PageSize]
		next = out[PageSize-1].ID
	}
	return out, next, nil
}

func ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// Settings answers the retention in days; 0 keeps events forever.
func Settings(ctx context.Context, q Querier) (int, error) {
	var days int
	err := q.QueryRowContext(ctx, "SELECT retention_days FROM audit_settings WHERE id = 1").Scan(&days)
	return days, err
}

// SetSettings stores the retention in days; 0 keeps events forever.
func SetSettings(ctx context.Context, db Execer, days int) error {
	if days < 0 {
		return ErrInvalidRetention
	}
	_, err := db.ExecContext(ctx, "UPDATE audit_settings SET retention_days = ? WHERE id = 1", days)
	return err
}

// Prune deletes the events older than the retention, and answers how many.
func Prune(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	days, err := Settings(ctx, db)
	if err != nil || days == 0 {
		return 0, err
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	result, err := db.ExecContext(ctx, "DELETE FROM audit_events WHERE at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
