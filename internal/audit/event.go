// Package audit records who did what, when and where in the panel
// (.scratch/multi-user/spec.md, Audit log; ADR-0031). It owns
// audit_events and audit_settings, and the per-request hooks handlers use to
// add detail or emit the reads only they can see. No variable value may
// ever be passed to it.
package audit

import "time"

// Outcomes and the CLI actor.
const (
	OutcomeOK     = "ok"
	OutcomeDenied = "denied"
	ActorCLI      = "cli"
)

// Event is one recorded action. Zero ids are stored as NULL.
type Event struct {
	At            time.Time
	ActorUserID   int64
	Actor         string
	Action        string
	Outcome       string
	ProjectID     int64
	EnvironmentID int64
	ComposeID     int64
	Target        string
	Detail        map[string]any
	IP            string
}

// Row is an event as the API answers it.
type Row struct {
	ID            int64          `json:"id"`
	At            int64          `json:"at"`
	ActorUserID   *int64         `json:"actorUserId"`
	Actor         string         `json:"actor"`
	Action        string         `json:"action"`
	Outcome       string         `json:"outcome"`
	ProjectID     *int64         `json:"projectId"`
	EnvironmentID *int64         `json:"environmentId"`
	ComposeID     *int64         `json:"composeId"`
	Target        string         `json:"target"`
	Detail        map[string]any `json:"detail"`
	IP            *string        `json:"ip"`
}
