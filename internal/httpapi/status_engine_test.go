package httpapi_test

import (
	"context"
	"database/sql"
	"strconv"

	"relist-panel/internal/composes"
)

// statusSource is a runner fake that knows each project's status.
type statusSource interface {
	statusFor(project string) composes.ComposeStatus
	recordCall(call fakeComposeRun)
}

// runnerEngine is the composes.ContainerEngine fake behind the status the
// Compose routes show: it turns the runner fake's statuses into containers,
// for every Compose in the database (project relist-c<id>). It records each
// read as a "status" call on the runner, so tests can see when Docker was asked.
type runnerEngine struct {
	source statusSource
	db     *sql.DB
}

// engineFor is the fake Engine over runner, or nil when runner has no statuses.
func engineFor(runner composes.ComposeRunner, db *sql.DB) composes.ContainerEngine {
	source, ok := runner.(statusSource)
	if !ok {
		return nil
	}
	return &runnerEngine{source: source, db: db}
}

func containersOf(project string, status composes.ComposeStatus) []composes.EngineContainer {
	total := max(status.Total, len(status.Services))
	containers := make([]composes.EngineContainer, 0, total)
	for i := range total {
		c := composes.EngineContainer{ID: project + "-" + strconv.Itoa(i), Project: project, Running: i < status.Running}
		if len(status.Services) > 0 {
			c.Service = status.Services[i%len(status.Services)]
		}
		containers = append(containers, c)
	}
	return containers
}

func (e *runnerEngine) ProjectContainers(_ context.Context, project string) ([]composes.EngineContainer, error) {
	e.source.recordCall(fakeComposeRun{method: "status", request: composes.RunRequest{ProjectName: project}})
	return containersOf(project, e.source.statusFor(project)), nil
}

func (e *runnerEngine) ComposeContainers(ctx context.Context) ([]composes.EngineContainer, error) {
	e.source.recordCall(fakeComposeRun{method: "status"})
	rows, err := e.db.QueryContext(ctx, `SELECT id FROM composes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []composes.EngineContainer
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		project := "relist-c" + strconv.FormatInt(id, 10)
		all = append(all, containersOf(project, e.source.statusFor(project))...)
	}
	return all, rows.Err()
}

func (*runnerEngine) Inspect(context.Context, string) (composes.EngineState, error) {
	return composes.EngineState{Running: true}, nil
}
func (*runnerEngine) Stop(context.Context, string) error               { return nil }
func (*runnerEngine) Remove(context.Context, string, bool, bool) error { return nil }
