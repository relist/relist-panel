package databases

import (
	"context"
	"fmt"
	"strings"
)

// dbState is what a one-database dump leaves out and a restore must put
// back: Prev is InspectSQL's line (owner and ACL for Postgres, charset and
// collation for MySQL and MariaDB), Settings the Postgres statements of
// dbSettingsQuery (ALTER ROLE … IN DATABASE … SET, ALTER DATABASE … SET).
type dbState struct {
	Name, Prev, Settings string
}

// captureState reads a logical database's state on the running server.
func (s *Service) captureState(ctx context.Context, d Database, dp Dumper, name string) (dbState, string, error) {
	prev, log, err := s.admin(ctx, d, dp, dp.InspectSQL(name))
	if err != nil {
		return dbState{}, log, err
	}
	st := dbState{Name: name, Prev: strings.TrimSpace(prev)}
	// A database that does not exist yet has no settings to keep.
	if d.Engine == "postgres" && st.Prev != "" {
		settings, log, err := s.admin(ctx, d, dp, dbSettingsQuery(name))
		if err != nil {
			return dbState{}, log, err
		}
		if settings = strings.TrimSpace(settings); settings != "" {
			st.Settings = settings + "\n"
		}
	}
	return st, "", nil
}

// restoreInto drops and recreates st.Name with its owner and ACL, restores
// the plain or gzipped SQL file at path into it, then puts its settings
// back. log is the failing step's stderr tail, or the restore's on success.
func (s *Service) restoreInto(ctx context.Context, d Database, dp Dumper, st dbState, path string) (string, error) {
	if _, log, err := s.admin(ctx, d, dp, dp.ReplaceSQL(st.Name, st.Prev)); err != nil {
		return log, err
	}
	in, c, err := openSQL(path)
	if err != nil {
		return "", err
	}
	defer c.Close()
	cmd, extra := dp.RestoreCmd(st.Name)
	errOut := &tailBuffer{max: logLimit}
	code, err := s.d.Host.Exec(ctx, ContainerName(d.ID), ExecRequest{Cmd: cmd, Env: execEnv(dp, d.Password, extra), Stdin: in, Stderr: errOut})
	if err != nil {
		return errOut.String(), err
	}
	if code != 0 {
		return errOut.String(), fmt.Errorf("the restore command exited with code %d", code)
	}
	if st.Settings != "" {
		if _, log, err := s.admin(ctx, d, dp, st.Settings); err != nil {
			return log, fmt.Errorf("restore the per-database settings: %w", err)
		}
	}
	return errOut.String(), nil
}
