package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// WorkNumbering implements apiserver.WorkNumbering through the work's media type.
type WorkNumbering struct {
	Works    library.Works
	Registry *media.Registry
}

func (a WorkNumbering) renumbering(ctx context.Context, workID int64) (media.Renumbering, error) {
	mediaType, err := a.Works.MediaTypeOf(ctx, workID)
	if err != nil {
		return nil, err
	}
	renumbering, ok := media.Capability[media.Renumbering](a.Registry, media.Key(mediaType))
	if !ok {
		return nil, apiserver.ErrNoNumbering
	}
	return renumbering, nil
}

// Choices implements apiserver.WorkNumbering.
func (a WorkNumbering) Choices(ctx context.Context, workID int64) ([]apiserver.MediaChoice, string, error) {
	renumbering, err := a.renumbering(ctx, workID)
	if err != nil {
		return nil, "", err
	}
	choices, current, err := renumbering.NumberingChoices(ctx, workID)
	if err != nil {
		return nil, "", noNumbering(err)
	}
	if len(choices) < 2 {
		return nil, "", apiserver.ErrNoNumbering
	}
	views := make([]apiserver.MediaChoice, len(choices))
	for i, choice := range choices {
		views[i] = apiserver.MediaChoice{Key: choice.Key, Label: choice.Label}
	}
	return views, current, nil
}

// Renumber implements apiserver.WorkNumbering.
func (a WorkNumbering) Renumber(ctx context.Context, workID int64, basis string, preview bool) (gen.NumberingPlan, error) {
	renumbering, err := a.renumbering(ctx, workID)
	if err != nil {
		return gen.NumberingPlan{}, err
	}
	var plan media.NumberingPlan
	if preview {
		plan, err = renumbering.PlanNumbering(ctx, workID, basis)
	} else {
		plan, err = renumbering.ApplyNumbering(ctx, workID, basis)
	}
	if err != nil {
		return gen.NumberingPlan{}, noNumbering(err)
	}
	return gen.NumberingPlan{Moves: moveViews(plan.Moves), SetAside: moveViews(plan.SetAside), Unchanged: plan.Unchanged, New: plan.New}, nil
}

// noNumbering turns the media type's answers into the API's.
func noNumbering(err error) error {
	switch {
	case errors.Is(err, media.ErrNotWired), errors.Is(err, media.ErrNoNumbering):
		return fmt.Errorf("%w: %w", apiserver.ErrNoNumbering, err)
	case errors.Is(err, media.ErrUnknownNumbering):
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	default:
		return err
	}
}

func moveViews(moves []media.NumberingMove) []gen.NumberingMove {
	views := make([]gen.NumberingMove, len(moves))
	for i, move := range moves {
		views[i] = gen.NumberingMove{Title: move.Title, From: move.From, HasFile: move.HasFile}
		if move.To != "" {
			to := move.To
			views[i].To = &to
		}
	}
	return views
}
