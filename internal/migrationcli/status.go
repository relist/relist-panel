package migrationcli

import (
	"fmt"
	"strings"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationtui"
)

// FormatStatus is `relist-panel migration status` as plain text: the new
// host's pull when there is one, else the old host's lock, else none.
func FormatStatus(p migration.Progress, s migration.OldHostStatus, a migration.Activity) string {
	var out []string
	switch {
	case p.Phase != migration.PhaseIdle:
		out = append(out, fmt.Sprintf("Migration from %s: %s", p.SourceURL, p.Phase))
		if p.LockStep != migration.StepNone && p.Phase != migration.PhaseDone {
			out = append(out, "Old server: "+string(p.LockStep))
		}
		if len(p.Items) > 0 {
			done := 0
			for _, it := range p.Items {
				if it.State == migration.ItemDone {
					done++
				}
			}
			out = append(out, fmt.Sprintf("Items: %d/%d done", done, len(p.Items)))
		}
		if p.Phase == migration.PhaseFinalizing && p.FinalizeStep != "" {
			out = append(out, "Finalize: "+string(p.FinalizeStep))
		}
		if p.Error != "" {
			out = append(out, "Error: "+p.Error)
		}
		if n := len(p.Log); n > 0 {
			e := p.Log[n-1]
			out = append(out, "Last: "+e.At.Local().Format("15:04:05")+" "+e.Message)
		}
		if p.Phase == migration.PhaseDone {
			// The last line says done, for scripts that tail it.
			out = append(out[1:], out[0])
		}
	case s.Step == migration.StepMigrated:
		out = append(out, "This server: "+migrationtui.MigratedTo(s)+". Run "+migration.TerminalCommand("resume")+" to use it again.")
	case s.Step != migration.StepNone:
		out = append(out, "This server is locked for a migration: "+string(s.Step))
		if s.ClaimedBy != "" {
			out = append(out, "Claimed by "+s.ClaimedBy)
		}
		if line := migrationtui.ActivityLine(a); line != "" {
			out = append(out, line)
		}
	case s.TokenExpiresAt != nil:
		line := "A migration token is live until " + s.TokenExpiresAt.Local().Format("2006-01-02 15:04")
		if s.ClaimedBy != "" {
			line += ", claimed by " + s.ClaimedBy
		}
		out = append(out, line+".")
	default:
		out = append(out, "No migration on this host.")
	}
	return strings.Join(out, "\n")
}
