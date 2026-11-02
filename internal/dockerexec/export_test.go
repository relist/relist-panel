package dockerexec

import (
	"context"
	"io"
)

var UpdateArgv = updateArgv

func NewPanelSelfNamed(socketPath, hostname string) *PanelSelf {
	return &PanelSelf{engine: NewEngine(socketPath), hostname: func() (string, error) { return hostname, nil }}
}

func NewPanelUpdateRunnerWith(run func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error) *PanelUpdateRunner {
	return &PanelUpdateRunner{runDocker: run}
}

func NewPanelDockerAt(socketPath string) *PanelDocker { return NewPanelDocker(socketPath) }

func NewPanelUpdaterHostWith(run func(ctx context.Context, args []string) ([]byte, error)) *PanelUpdaterHost {
	return &PanelUpdaterHost{runCompose: run}
}

var ExecComposeForTest = execCompose
