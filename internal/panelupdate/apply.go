package panelupdate

import "context"

// unticked lists the manual steps the operator did not confirm.
func unticked(steps []ManualStep, done []string) []string {
	have := map[string]bool{}
	for _, id := range done {
		have[id] = true
	}
	var missing []string
	for _, st := range steps {
		if !have[st.ID] {
			missing = append(missing, st.ID)
		}
	}
	return missing
}

// OwnerName is who a prune plan says the previous panel image belongs to.
const OwnerName = "Panel update"

// PreviousImage is the image ID the panel ran from before its last successful
// update. A prune plan never offers it: it is what a manual rollback runs.
func (s *Service) PreviousImage(ctx context.Context) (string, error) {
	return s.d.Store.PreviousImage(ctx)
}
