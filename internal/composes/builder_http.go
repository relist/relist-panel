package composes

import (
	"context"
	"errors"
	"net/http"

	"relist-panel/internal/builders"
)

// builderChoices is each Compose's Builder choice; builders.Store implements it.
type builderChoices interface {
	ComposeChoice(ctx context.Context, composeID int64) (builders.Choice, error)
	SetComposeChoice(ctx context.Context, composeID int64, c builders.Choice) error
	Resolve(ctx context.Context, composeID int64) (int64, error)
}

type builderChoiceJSON struct {
	Mode      string `json:"mode"`
	BuilderID int64  `json:"builderId"`
}

func (s *Service) getBuilder(w http.ResponseWriter, r *http.Request) {
	if s.builders == nil {
		http.NotFound(w, r)
		return
	}
	compose, ok := s.zeroDowntimeCompose(w, r)
	if !ok {
		return
	}
	c, err := s.builders.ComposeChoice(r.Context(), compose.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the builder choice")
		return
	}
	writeJSON(w, http.StatusOK, builderChoiceJSON{Mode: c.Mode, BuilderID: c.BuilderID})
}

func (s *Service) putBuilder(w http.ResponseWriter, r *http.Request) {
	if s.builders == nil {
		http.NotFound(w, r)
		return
	}
	compose, ok := s.zeroDowntimeCompose(w, r)
	if !ok {
		return
	}
	var input builderChoiceJSON
	if err := decodeInput(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.builders.SetComposeChoice(r.Context(), compose.ID, builders.Choice{Mode: input.Mode, BuilderID: input.BuilderID})
	if errors.Is(err, builders.ErrUnknownBuilder) {
		writeError(w, http.StatusUnprocessableEntity, "builder not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the builder choice")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
